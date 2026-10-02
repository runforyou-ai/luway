//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
	"uuid"
)

// AgentMemoryActionName 根据个人 AI 员工单聊的新消息更新个人 AI 员工记忆。
const AgentMemoryActionName = "agent_memory.extract"

const (
	// agentMemoryTimeout 限制一次记忆提取的执行时长。
	agentMemoryTimeout = 2 * time.Minute
	// agentMemoryMaxAttempts 是记忆提取任务的最大尝试次数。
	agentMemoryMaxAttempts = 3
	// agentMemoryRecentLimit 是一次提取分析的新消息条数上限，其余新消息由后续任务继续提取。
	agentMemoryRecentLimit = 40
	// agentMemoryEarlierLimit 是随新消息提供的已提取消息条数。
	agentMemoryEarlierLimit = 6
	// agentMemoryMessageMaxRunes 是单条消息进入提取资料的最大字符数。
	agentMemoryMessageMaxRunes = 2000
)

// AgentMemoryInput 定义一次记忆提取任务，提取范围在执行时按会话的提取进度确定。
type AgentMemoryInput struct {
	OrganizationID string `json:"organizationId"`
	ConversationID string `json:"conversationId"`
}

// ExtractAgentMemoryAction 使用个人 AI 员工当前模型从单聊新消息中提取并保存记忆。
type ExtractAgentMemoryAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	extractor agentruntime.MemoryExtractor
	invoker   *modelcall.Invoker
}

// errAgentMemoryChanged 表示提取期间会话的提取进度或个人 AI 员工的记忆已被其他任务或负责人修改，本次结果作废并重试。
var errAgentMemoryChanged = errors.New("agent memory changed during extraction")

// NewExtractAgentMemoryAction 创建个人 AI 员工记忆提取操作。
func NewExtractAgentMemoryAction(db *bun.DB, enqueuer servertask.TxEnqueuer, extractor agentruntime.MemoryExtractor, invoker *modelcall.Invoker) *ExtractAgentMemoryAction {
	return &ExtractAgentMemoryAction{db: db, enqueuer: enqueuer, extractor: extractor, invoker: invoker}
}

// enqueueAgentMemory 在调用方事务中确认运行的 Agent 是个人 AI 员工，是时为回复消息投递记忆提取任务。
func enqueueAgentMemory(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, run *servermodels.AgentRun, messageID string) error {
	personal, err := db.NewSelect().Model((*servermodels.Agent)(nil)).
		Where("a.organization_id = ? AND a.identity_id = ? AND ? = ANY(a.service_audiences)", run.OrganizationID, run.AgentIdentityID, domain.ServiceAudiencePersonal).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check personal agent for memory extraction: %w", err)
	}
	if !personal {
		return nil
	}
	return enqueueAgentMemoryExtraction(ctx, db, enqueuer, AgentMemoryInput{OrganizationID: run.OrganizationID, ConversationID: run.ConversationID}, "agent-memory:"+messageID)
}

// enqueueAgentMemoryExtraction 在调用方事务中把记忆提取任务投递到 Agent 队列。
func enqueueAgentMemoryExtraction(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, input AgentMemoryInput, idempotencyKey string) error {
	options := servertask.EnqueueOptions{Queue: servertask.QueueAgent, MaxAttempts: agentMemoryMaxAttempts, IdempotencyKey: idempotencyKey}
	if _, err := enqueuer.EnqueueIn(ctx, db, AgentMemoryActionName, input, options); err != nil {
		return fmt.Errorf("enqueue agent memory extraction: %w", err)
	}
	return nil
}

// loadAgentMemoryEntries 读取个人 AI 员工的全部记忆条目。
func loadAgentMemoryEntries(ctx context.Context, db bun.IDB, organizationID, agentID string) ([]agentruntime.MemoryEntry, error) {
	var rows []servermodels.AgentMemory
	if err := db.NewSelect().Model(&rows).
		Where("am.organization_id = ? AND am.agent_id = ?", organizationID, agentID).
		OrderExpr("am.updated_at DESC, am.path ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load agent memories: %w", err)
	}
	entries := make([]agentruntime.MemoryEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, agentruntime.MemoryEntry{Path: row.Path, Name: row.Name, Description: row.Description, Body: row.Body, UpdatedAt: row.UpdatedAt})
	}
	return entries, nil
}

// LoadDeviceRunMemory 读取设备持有运行所属个人 AI 员工的记忆，有效配置未启用记忆时返回空。
func (a *ExecuteAction) LoadDeviceRunMemory(ctx context.Context, device RunDevice, runID string) ([]agentruntime.MemoryEntry, error) {
	run, err := a.requireDeviceLease(ctx, device, runID)
	if err != nil {
		return nil, err
	}
	var assignment agentruntime.Assignment
	if len(run.BehaviorSnapshot) > 0 {
		if err := json.Unmarshal(run.BehaviorSnapshot, &assignment); err != nil {
			return nil, fmt.Errorf("decode device agent run assignment: %w", err)
		}
	}
	if !assignment.Memory {
		return []agentruntime.MemoryEntry{}, nil
	}
	var agentID string
	if err := a.db.NewSelect().Model((*servermodels.Agent)(nil)).Column("id").
		Where("a.organization_id = ? AND a.identity_id = ?", run.OrganizationID, run.AgentIdentityID).
		Scan(ctx, &agentID); err != nil {
		return nil, fmt.Errorf("load device agent run personal agent: %w", err)
	}
	return loadAgentMemoryEntries(ctx, a.db, run.OrganizationID, agentID)
}

// Execute 从提取进度之后最早的新消息起分析一批，把记忆变更与新的提取进度在同一事务中写入并通知负责人，仍有未提取的消息时投递下一批；会话不是个人 AI 员工单聊、个人 AI 员工已停用或没有有效托管配置时不提取。
// 提取期间会话进度被推进，或个人 AI 员工的记忆被其他会话的提取或负责人修改时，本次结果作废并返回错误，重试时基于最新的记忆重新提取。
func (a *ExtractAgentMemoryAction) Execute(ctx context.Context, input AgentMemoryInput) error {
	var agent struct {
		managedAgentModel
		AgentID               string `bun:"agent_id"`
		AgentIdentityID       string `bun:"agent_identity_id"`
		ResponsibleUserID     string `bun:"responsible_user_id"`
		ResponsibleIdentityID string `bun:"responsible_identity_id"`
		MemoryExtractedSeq    int64  `bun:"memory_extracted_seq"`
	}
	err := a.db.NewSelect().TableExpr("agent_conversations AS ac").
		ColumnExpr("a.id AS agent_id, a.identity_id AS agent_identity_id, a.responsible_user_id, ac.user_identity_id AS responsible_identity_id, ac.memory_extracted_seq").
		Join("JOIN agents AS a ON a.identity_id = ac.agent_identity_id AND a.organization_id = ac.organization_id").
		Apply(func(query *bun.SelectQuery) *bun.SelectQuery {
			return withManagedAgentConfiguration(query, "a.active_revision_id")
		}).
		Where("ac.organization_id = ? AND ac.conversation_id = ?", input.OrganizationID, input.ConversationID).
		Where("? = ANY(a.service_audiences) AND a.status = ?", domain.ServiceAudiencePersonal, domain.IdentityStatusActive).
		Scan(ctx, &agent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load agent memory conversation: %w", err)
	}
	recent, throughSeq, err := loadAgentMemoryMessages(ctx, a.db, input, agent.ResponsibleIdentityID, "msg.message_seq > ?", agent.MemoryExtractedSeq, "ASC", agentMemoryRecentLimit)
	if err != nil || len(recent) == 0 {
		return err
	}
	earlier, _, err := loadAgentMemoryMessages(ctx, a.db, input, agent.ResponsibleIdentityID, "msg.message_seq <= ?", agent.MemoryExtractedSeq, "DESC", agentMemoryEarlierLimit)
	if err != nil {
		return err
	}
	entries, err := loadAgentMemoryEntries(ctx, a.db, input.OrganizationID, agent.AgentID)
	if err != nil {
		return err
	}
	scope := modelcall.AgentScope(input.OrganizationID, agent.AgentIdentityID, domain.AIModelCallSourceConversation, input.ConversationID)
	model, err := agent.modelConfig(ctx, a.db, a.invoker, scope)
	if errors.Is(err, aimodel.ErrUnavailable) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve agent memory model: %w", err)
	}
	extractCtx, cancel := context.WithTimeout(ctx, agentMemoryTimeout)
	defer cancel()
	result, err := a.extractor.ExtractMemory(extractCtx, agentruntime.MemoryExtractionRequest{
		Model: model, Entries: entries, Earlier: earlier, Recent: recent,
	})
	if err != nil {
		return fmt.Errorf("extract agent memory: %w", err)
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewSelect().Model((*servermodels.Agent)(nil)).Column("id").
			Where("a.organization_id = ? AND a.id = ?", input.OrganizationID, agent.AgentID).
			For("UPDATE").Exec(ctx); err != nil {
			return fmt.Errorf("lock agent for memory: %w", err)
		}
		var progress int64
		if err := tx.NewSelect().Model((*servermodels.AgentConversation)(nil)).Column("memory_extracted_seq").
			Where("organization_id = ? AND conversation_id = ?", input.OrganizationID, input.ConversationID).
			For("UPDATE").Scan(ctx, &progress); err != nil {
			return fmt.Errorf("lock agent memory progress: %w", err)
		}
		current, err := loadAgentMemoryEntries(ctx, tx, input.OrganizationID, agent.AgentID)
		if err != nil {
			return err
		}
		// 提取开始后进度或任一条目的路径、更新时间发生变化时作废本次结果。
		if progress != agent.MemoryExtractedSeq || !slices.EqualFunc(current, entries, func(a, b agentruntime.MemoryEntry) bool {
			return a.Path == b.Path && a.UpdatedAt.Equal(b.UpdatedAt)
		}) {
			return errAgentMemoryChanged
		}
		// 同一路径保留最后一次写入，整批一条语句写入。
		memories := make([]*servermodels.AgentMemory, 0, len(result.Saved))
		positions := make(map[string]int, len(result.Saved))
		for _, entry := range result.Saved {
			memory := &servermodels.AgentMemory{
				ID: uuid.NewV7().String(), OrganizationID: input.OrganizationID, AgentID: agent.AgentID,
				Path: entry.Path, Name: entry.Name, Description: entry.Description, Body: entry.Body,
			}
			if position, exists := positions[entry.Path]; exists {
				memories[position] = memory
				continue
			}
			positions[entry.Path] = len(memories)
			memories = append(memories, memory)
		}
		if len(memories) > 0 {
			if _, err := tx.NewInsert().Model(&memories).
				Column("id", "organization_id", "agent_id", "path", "name", "description", "body").
				On("CONFLICT (agent_id, path) DO UPDATE").
				Set("name = EXCLUDED.name, description = EXCLUDED.description, body = EXCLUDED.body, updated_at = now()").
				Exec(ctx); err != nil {
				return fmt.Errorf("save agent memory: %w", err)
			}
		}
		if len(result.Deleted) > 0 {
			if _, err := tx.NewDelete().Model((*servermodels.AgentMemory)(nil)).
				Where("organization_id = ? AND agent_id = ? AND path IN (?)", input.OrganizationID, agent.AgentID, bun.In(result.Deleted)).
				Exec(ctx); err != nil {
				return fmt.Errorf("delete agent memory: %w", err)
			}
		}
		if len(result.Saved) > 0 || len(result.Deleted) > 0 {
			realtime.Notify(ctx, realtime.UserAgentMemoryChanged(input.OrganizationID, agent.ResponsibleUserID, agent.AgentID))
		}
		if _, err := tx.NewUpdate().Model((*servermodels.AgentConversation)(nil)).
			Set("memory_extracted_seq = ?", throughSeq).
			Set("updated_at = now()").
			Where("organization_id = ? AND conversation_id = ?", input.OrganizationID, input.ConversationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("advance agent memory progress: %w", err)
		}
		// 本批之后仍有新消息时投递下一批。
		more, err := tx.NewSelect().Model((*servermodels.Message)(nil)).
			Where("organization_id = ? AND conversation_id = ? AND message_seq > ?", input.OrganizationID, input.ConversationID, throughSeq).
			Where("type IN (?) AND visibility = ? AND deleted_at IS NULL", bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment}), domain.MessageVisibilityShared).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check remaining agent memory messages: %w", err)
		}
		if more {
			if err := enqueueAgentMemoryExtraction(ctx, tx, a.enqueuer, input, fmt.Sprintf("agent-memory:%s:%d", input.ConversationID, throughSeq)); err != nil {
				return err
			}
		}
		slog.Info("个人 AI 员工记忆已提取", "organization_id", input.OrganizationID, "conversation_id", input.ConversationID,
			"through_seq", throughSeq, "saved", len(result.Saved), "deleted", len(result.Deleted), "total_tokens", result.Usage.TotalTokens)
		return nil
	})
}

// loadAgentMemoryMessages 按序号条件和序号方向读取单聊中不超过 limit 条的文本与附件消息，按时间顺序返回并给出其中最大的消息序号；order 为 ASC 时取最早的消息，为 DESC 时取最近的消息。
func loadAgentMemoryMessages(ctx context.Context, db bun.IDB, input AgentMemoryInput, ownerIdentityID, seqCondition string, seq int64, order string, limit int) ([]agentruntime.MemoryMessage, int64, error) {
	var rows []struct {
		MessageSeq     int64  `bun:"message_seq"`
		Body           string `bun:"body"`
		SenderSourceID string `bun:"sender_source_id"`
	}
	if err := db.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.message_seq").
		ColumnExpr("? AS body", messagequery.Summary("msg")).
		ColumnExpr("cs.source_id AS sender_source_id").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
		Where("msg.organization_id = ? AND msg.conversation_id = ?", input.OrganizationID, input.ConversationID).
		Where("msg.type IN (?) AND msg.visibility = ? AND msg.deleted_at IS NULL", bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment}), domain.MessageVisibilityShared).
		Where(seqCondition, seq).
		OrderExpr("msg.message_seq "+order).
		Limit(limit).
		Scan(ctx, &rows); err != nil {
		return nil, 0, fmt.Errorf("load agent memory messages: %w", err)
	}
	if order == "DESC" {
		slices.Reverse(rows)
	}
	messages := make([]agentruntime.MemoryMessage, 0, len(rows))
	var throughSeq int64
	for _, row := range rows {
		sender := "assistant"
		if row.SenderSourceID == ownerIdentityID {
			sender = "owner"
		}
		content := []rune(strings.TrimSpace(row.Body))
		if len(content) > agentMemoryMessageMaxRunes {
			content = content[:agentMemoryMessageMaxRunes]
		}
		messages = append(messages, agentruntime.MemoryMessage{Sender: sender, Content: string(content)})
		throughSeq = max(throughSeq, row.MessageSeq)
	}
	return messages, throughSeq, nil
}
