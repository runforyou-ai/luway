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

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
	"uuid"
)

// AssistantMemoryActionName 根据助理单聊的新消息更新助理记忆。
const AssistantMemoryActionName = "assistant_memory.extract"

const (
	// assistantMemoryTimeout 限制一次记忆提取的执行时长。
	assistantMemoryTimeout = 2 * time.Minute
	// assistantMemoryMaxAttempts 是记忆提取任务的最大尝试次数。
	assistantMemoryMaxAttempts = 3
	// assistantMemoryRecentLimit 是一次提取分析的新消息条数上限，其余新消息由后续任务继续提取。
	assistantMemoryRecentLimit = 40
	// assistantMemoryEarlierLimit 是随新消息提供的已提取消息条数。
	assistantMemoryEarlierLimit = 6
	// assistantMemoryMessageMaxRunes 是单条消息进入提取资料的最大字符数。
	assistantMemoryMessageMaxRunes = 2000
)

// AssistantMemoryInput 定义一次记忆提取任务，提取范围在执行时按会话的提取进度确定。
type AssistantMemoryInput struct {
	OrganizationID string `json:"organizationId"`
	ConversationID string `json:"conversationId"`
}

// ExtractAssistantMemoryAction 使用助理当前模型从单聊新消息中提取并保存记忆。
type ExtractAssistantMemoryAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	extractor agentruntime.MemoryExtractor
}

// errAssistantMemoryChanged 表示提取期间会话的提取进度或助理的记忆已被其他任务或主人修改，本次结果作废并重试。
var errAssistantMemoryChanged = errors.New("assistant memory changed during extraction")

// NewExtractAssistantMemoryAction 创建助理记忆提取操作。
func NewExtractAssistantMemoryAction(db *bun.DB, enqueuer servertask.TxEnqueuer, extractor agentruntime.MemoryExtractor) *ExtractAssistantMemoryAction {
	return &ExtractAssistantMemoryAction{db: db, enqueuer: enqueuer, extractor: extractor}
}

// enqueueAssistantMemory 在调用方事务中确认运行的 Agent 是助理，是时为回复消息投递记忆提取任务。
func enqueueAssistantMemory(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, run *servermodels.AgentRun, messageID string) error {
	assistant, err := db.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).
		Where("organization_id = ? AND id = ? AND type = ?", run.OrganizationID, run.AgentIdentityID, domain.OrganizationIdentityTypeAssistant).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check assistant for memory extraction: %w", err)
	}
	if !assistant {
		return nil
	}
	return enqueueAssistantMemoryExtraction(ctx, db, enqueuer, AssistantMemoryInput{OrganizationID: run.OrganizationID, ConversationID: run.ConversationID}, "assistant-memory:"+messageID)
}

// enqueueAssistantMemoryExtraction 在调用方事务中把记忆提取任务投递到 Agent 队列。
func enqueueAssistantMemoryExtraction(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, input AssistantMemoryInput, idempotencyKey string) error {
	options := servertask.EnqueueOptions{Queue: servertask.QueueAgent, MaxAttempts: assistantMemoryMaxAttempts, IdempotencyKey: idempotencyKey}
	if _, err := enqueuer.EnqueueIn(ctx, db, AssistantMemoryActionName, input, options); err != nil {
		return fmt.Errorf("enqueue assistant memory extraction: %w", err)
	}
	return nil
}

// loadAssistantMemoryEntries 读取助理的全部记忆条目。
func loadAssistantMemoryEntries(ctx context.Context, db bun.IDB, organizationID, agentID string) ([]agentruntime.MemoryEntry, error) {
	var rows []servermodels.AssistantMemory
	if err := db.NewSelect().Model(&rows).
		Where("am.organization_id = ? AND am.agent_id = ?", organizationID, agentID).
		OrderExpr("am.updated_at DESC, am.path ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load assistant memories: %w", err)
	}
	entries := make([]agentruntime.MemoryEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, agentruntime.MemoryEntry{Path: row.Path, Name: row.Name, Description: row.Description, Body: row.Body, UpdatedAt: row.UpdatedAt})
	}
	return entries, nil
}

// LoadDeviceRunMemory 读取设备持有运行所属助理的记忆，有效配置未启用记忆时返回空。
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
		return nil, fmt.Errorf("load device agent run assistant: %w", err)
	}
	return loadAssistantMemoryEntries(ctx, a.db, run.OrganizationID, agentID)
}

// Execute 从提取进度之后最早的新消息起分析一批，把记忆变更与新的提取进度在同一事务中写入并通知主人，仍有未提取的消息时投递下一批；会话不是助理单聊、助理已停用或没有有效托管配置时不提取。
// 提取期间会话进度被推进，或助理的记忆被其他会话的提取或主人修改时，本次结果作废并返回错误，重试时基于最新的记忆重新提取。
func (a *ExtractAssistantMemoryAction) Execute(ctx context.Context, input AssistantMemoryInput) error {
	var assistant struct {
		managedAgentModel
		AgentID            string `bun:"agent_id"`
		OwnerUserID        string `bun:"owner_user_id"`
		OwnerIdentityID    string `bun:"owner_identity_id"`
		MemoryExtractedSeq int64  `bun:"memory_extracted_seq"`
	}
	err := a.db.NewSelect().TableExpr("agent_conversations AS ac").
		ColumnExpr("a.id AS agent_id, a.owner_user_id, ac.user_identity_id AS owner_identity_id, ac.memory_extracted_seq").
		Join("JOIN agents AS a ON a.identity_id = ac.agent_identity_id AND a.organization_id = ac.organization_id").
		Apply(func(query *bun.SelectQuery) *bun.SelectQuery {
			return withManagedAgentConfiguration(query, "a.active_revision_id")
		}).
		Where("ac.organization_id = ? AND ac.conversation_id = ?", input.OrganizationID, input.ConversationID).
		Where("oi.type = ? AND a.status = ?", domain.OrganizationIdentityTypeAssistant, domain.IdentityStatusActive).
		Scan(ctx, &assistant)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load assistant memory conversation: %w", err)
	}
	recent, throughSeq, err := loadAssistantMemoryMessages(ctx, a.db, input, assistant.OwnerIdentityID, "msg.message_seq > ?", assistant.MemoryExtractedSeq, "ASC", assistantMemoryRecentLimit)
	if err != nil || len(recent) == 0 {
		return err
	}
	earlier, _, err := loadAssistantMemoryMessages(ctx, a.db, input, assistant.OwnerIdentityID, "msg.message_seq <= ?", assistant.MemoryExtractedSeq, "DESC", assistantMemoryEarlierLimit)
	if err != nil {
		return err
	}
	entries, err := loadAssistantMemoryEntries(ctx, a.db, input.OrganizationID, assistant.AgentID)
	if err != nil {
		return err
	}
	extractCtx, cancel := context.WithTimeout(ctx, assistantMemoryTimeout)
	defer cancel()
	result, err := a.extractor.ExtractMemory(extractCtx, agentruntime.MemoryExtractionRequest{
		Model: assistant.modelConfig(), Entries: entries, Earlier: earlier, Recent: recent,
	})
	if err != nil {
		return fmt.Errorf("extract assistant memory: %w", err)
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewSelect().Model((*servermodels.Agent)(nil)).Column("id").
			Where("a.organization_id = ? AND a.id = ?", input.OrganizationID, assistant.AgentID).
			For("UPDATE").Exec(ctx); err != nil {
			return fmt.Errorf("lock assistant for memory: %w", err)
		}
		var progress int64
		if err := tx.NewSelect().Model((*servermodels.AgentConversation)(nil)).Column("memory_extracted_seq").
			Where("organization_id = ? AND conversation_id = ?", input.OrganizationID, input.ConversationID).
			For("UPDATE").Scan(ctx, &progress); err != nil {
			return fmt.Errorf("lock assistant memory progress: %w", err)
		}
		current, err := loadAssistantMemoryEntries(ctx, tx, input.OrganizationID, assistant.AgentID)
		if err != nil {
			return err
		}
		// 提取开始后进度或任一条目的路径、更新时间发生变化时作废本次结果。
		if progress != assistant.MemoryExtractedSeq || !slices.EqualFunc(current, entries, func(a, b agentruntime.MemoryEntry) bool {
			return a.Path == b.Path && a.UpdatedAt.Equal(b.UpdatedAt)
		}) {
			return errAssistantMemoryChanged
		}
		// 同一路径保留最后一次写入，整批一条语句写入。
		memories := make([]*servermodels.AssistantMemory, 0, len(result.Saved))
		positions := make(map[string]int, len(result.Saved))
		for _, entry := range result.Saved {
			memory := &servermodels.AssistantMemory{
				ID: uuid.NewV7().String(), OrganizationID: input.OrganizationID, AgentID: assistant.AgentID,
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
				return fmt.Errorf("save assistant memory: %w", err)
			}
		}
		if len(result.Deleted) > 0 {
			if _, err := tx.NewDelete().Model((*servermodels.AssistantMemory)(nil)).
				Where("organization_id = ? AND agent_id = ? AND path IN (?)", input.OrganizationID, assistant.AgentID, bun.In(result.Deleted)).
				Exec(ctx); err != nil {
				return fmt.Errorf("delete assistant memory: %w", err)
			}
		}
		if len(result.Saved) > 0 || len(result.Deleted) > 0 {
			realtime.Notify(ctx, realtime.UserAssistantMemoryChanged(input.OrganizationID, assistant.OwnerUserID, assistant.AgentID))
		}
		if _, err := tx.NewUpdate().Model((*servermodels.AgentConversation)(nil)).
			Set("memory_extracted_seq = ?", throughSeq).
			Set("updated_at = now()").
			Where("organization_id = ? AND conversation_id = ?", input.OrganizationID, input.ConversationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("advance assistant memory progress: %w", err)
		}
		// 本批之后仍有新消息时投递下一批。
		more, err := tx.NewSelect().Model((*servermodels.Message)(nil)).
			Where("organization_id = ? AND conversation_id = ? AND message_seq > ?", input.OrganizationID, input.ConversationID, throughSeq).
			Where("type IN (?) AND visibility = ? AND deleted_at IS NULL", bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment}), domain.MessageVisibilityShared).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check remaining assistant memory messages: %w", err)
		}
		if more {
			if err := enqueueAssistantMemoryExtraction(ctx, tx, a.enqueuer, input, fmt.Sprintf("assistant-memory:%s:%d", input.ConversationID, throughSeq)); err != nil {
				return err
			}
		}
		slog.Info("助理记忆已提取", "organization_id", input.OrganizationID, "conversation_id", input.ConversationID,
			"through_seq", throughSeq, "saved", len(result.Saved), "deleted", len(result.Deleted), "total_tokens", result.Usage.TotalTokens)
		return nil
	})
}

// loadAssistantMemoryMessages 按序号条件和序号方向读取单聊中不超过 limit 条的文本与附件消息，按时间顺序返回并给出其中最大的消息序号；order 为 ASC 时取最早的消息，为 DESC 时取最近的消息。
func loadAssistantMemoryMessages(ctx context.Context, db bun.IDB, input AssistantMemoryInput, ownerIdentityID, seqCondition string, seq int64, order string, limit int) ([]agentruntime.MemoryMessage, int64, error) {
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
		return nil, 0, fmt.Errorf("load assistant memory messages: %w", err)
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
		if len(content) > assistantMemoryMessageMaxRunes {
			content = content[:assistantMemoryMessageMaxRunes]
		}
		messages = append(messages, agentruntime.MemoryMessage{Sender: sender, Content: string(content)})
		throughSeq = max(throughSeq, row.MessageSeq)
	}
	return messages, throughSeq, nil
}
