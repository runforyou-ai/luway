//go:build server

package processquery

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// Reference 是已完成运行的过程引用和模型用量。
type Reference struct {
	ID                   string
	DurationMilliseconds int64
	Usage                agentcontract.Usage
	Outcome              *domain.AgentRunOutcome
	OutcomeReason        *domain.AgentHandoffReason
}

// LocalAgentTurn 是委派本机 Agent 的一轮：工具调用编号与本机 Agent 名称。
type LocalAgentTurn struct {
	ToolCallID string
	LocalAgent string
}

// Run 是尚未由结果消息表达的运行状态，已结束与挂起等待的运行携带自身过程引用。
type Run struct {
	AgentAvatarFileID *string
	AgentName         string
	// AgentPersonalResponsibleName 是执行者为个人 AI 员工时其负责人的名称，其他 AI 员工为空。
	AgentPersonalResponsibleName *string
	ID                           string
	AgentIdentityID              string
	Status                       domain.AgentRunStatus
	ErrorCode                    *string
	LastError                    *string
	Process                      *Reference
}

// PendingAgent 是已收到输入、等待轮转执行的 AI 员工。
type PendingAgent struct {
	IdentityID   string
	DisplayName  string
	AvatarFileID *string
	// PersonalResponsibleName 是等待者为个人 AI 员工时其负责人的名称，其他 AI 员工为空。
	PersonalResponsibleName *string
}

// MessageProcess 是一条 AI 回复消息对应运行的过程引用、交给本机 Agent 的轮次与失败原因。
type MessageProcess struct {
	Process         *Reference
	LocalAgentTurns []LocalAgentTurn
	ErrorCode       *domain.AgentRunErrorCode
}

// ConversationProcesses 是消息窗口中的运行过程读模型。
type ConversationProcesses struct {
	// Runs 是尚未由结果消息表达的运行，按创建顺序排列。
	Runs []Run
	// PendingAgents 是按发言顺序等待轮转执行的 AI 员工。
	PendingAgents []PendingAgent
	// Messages 按消息编号给出 AI 回复的运行过程，没有运行过程的消息不出现。
	Messages map[string]*MessageProcess
}

// LoadConversation 在已授权的消息窗口中批量读取已完成运行的过程引用与交给本机 Agent 的轮次、尚未由消息表达的运行状态和等待发言的 AI 员工。
func LoadConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string, messageIDs []string) (ConversationProcesses, error) {
	result := ConversationProcesses{Messages: map[string]*MessageProcess{}}
	var err error
	if result.Runs, err = loadConversationRuns(ctx, db, workspaceID, conversationID); err != nil {
		return ConversationProcesses{}, err
	}
	if result.PendingAgents, err = loadPendingAgents(ctx, db, workspaceID, conversationID); err != nil {
		return ConversationProcesses{}, err
	}
	if len(messageIDs) == 0 {
		return result, nil
	}
	// message 返回消息编号对应的运行过程，首次访问时创建。
	message := func(messageID string) *MessageProcess {
		if result.Messages[messageID] == nil {
			result.Messages[messageID] = &MessageProcess{}
		}
		return result.Messages[messageID]
	}
	var runs []servermodels.AgentRun
	if err := db.NewSelect().Model(&runs).
		Where("agr.workspace_id = ? AND agr.conversation_id = ?", workspaceID, conversationID).
		Where("agr.response_message_id IN (?)", bun.List(messageIDs)).
		Where("agr.started_at IS NOT NULL AND agr.completed_at IS NOT NULL").
		Where(runHasProcessCondition).Scan(ctx); err != nil {
		return ConversationProcesses{}, fmt.Errorf("load message agent processes: %w", err)
	}
	for _, run := range runs {
		process, err := runReference(&run)
		if err != nil {
			return ConversationProcesses{}, err
		}
		message(*run.ResponseMessageID).Process = process
	}
	// 为 AI 回复补充其运行交给本机 Agent 的轮次。
	var turns []struct {
		ResponseMessageID string `bun:"response_message_id"`
		ToolCallID        string `bun:"tool_call_id"`
		LocalAgent        string `bun:"local_agent"`
	}
	if err := db.NewSelect().TableExpr("agent_tool_calls AS atc").
		ColumnExpr("agr.response_message_id::text, atc.id::text AS tool_call_id, las.local_agent").
		Join("JOIN agent_runs AS agr ON agr.workspace_id = atc.workspace_id AND agr.id = atc.agent_run_id").
		Join("JOIN local_agent_sessions AS las ON las.workspace_id = atc.workspace_id AND las.id = atc.local_agent_session_id").
		Where("agr.workspace_id = ? AND agr.conversation_id = ? AND agr.response_message_id IN (?)", workspaceID, conversationID, bun.List(messageIDs)).
		Where("atc.source <> ?", domain.AgentToolSourceLocalAgent).
		OrderExpr("atc.id").Scan(ctx, &turns); err != nil {
		return ConversationProcesses{}, fmt.Errorf("load message local agent turns: %w", err)
	}
	for _, turn := range turns {
		process := message(turn.ResponseMessageID)
		process.LocalAgentTurns = append(process.LocalAgentTurns, LocalAgentTurn{ToolCallID: turn.ToolCallID, LocalAgent: turn.LocalAgent})
	}
	// 为 AI 回复失败消息补充对应运行的稳定失败原因。
	var failures []struct {
		ResponseMessageID string                   `bun:"response_message_id"`
		ErrorCode         domain.AgentRunErrorCode `bun:"error_code"`
	}
	if err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Column("response_message_id", "error_code").
		Where("agr.workspace_id = ? AND agr.conversation_id = ?", workspaceID, conversationID).
		Where("agr.response_message_id IN (?)", bun.List(messageIDs)).
		Where("agr.status = ? AND agr.error_code IS NOT NULL", domain.AgentRunStatusFailed).
		Scan(ctx, &failures); err != nil {
		return ConversationProcesses{}, fmt.Errorf("load message agent failures: %w", err)
	}
	for _, failure := range failures {
		message(failure.ResponseMessageID).ErrorCode = &failure.ErrorCode
	}
	return result, nil
}

// runHasProcessCondition 限定已持久化过程内容的运行，没有内容的运行不给出可展开的过程引用。
const runHasProcessCondition = `EXISTS (
	SELECT 1 FROM agent_run_blocks AS arb
	WHERE arb.workspace_id = agr.workspace_id AND arb.agent_run_id = agr.id
)`

// runDuration 返回运行从开始到结束的时长，挂起等待的运行计到最近一次更新。
func runDuration(run *servermodels.AgentRun) time.Duration {
	return support.DerefOr(run.CompletedAt, run.UpdatedAt).Sub(*run.StartedAt)
}

// runReference 按运行的起止时间、模型用量和结果构造过程引用。
func runReference(run *servermodels.AgentRun) (*Reference, error) {
	process := &Reference{ID: run.ID, DurationMilliseconds: runDuration(run).Milliseconds(),
		Outcome: (*domain.AgentRunOutcome)(run.Outcome), OutcomeReason: (*domain.AgentHandoffReason)(run.OutcomeReason)}
	if err := json.Unmarshal(run.Usage, &process.Usage); err != nil {
		return nil, fmt.Errorf("decode agent usage: %w", err)
	}
	return process, nil
}

// loadConversationRuns 读取尚未由结果消息表达的运行：仍在执行的运行，以及结束后会话再无新消息的取消运行。取消时间与消息创建时间同取数据库时钟。
func loadConversationRuns(ctx context.Context, db bun.IDB, workspaceID, conversationID string) ([]Run, error) {
	var rows []struct {
		servermodels.AgentRun        `bun:",embed"`
		AgentName                    string  `bun:"agent_name"`
		AgentPersonalResponsibleName *string `bun:"agent_personal_responsible_name"`
		AgentAvatarFileID            *string `bun:"agent_avatar_file_id"`
		HasProcess                   bool    `bun:"has_process"`
	}
	if err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		ColumnExpr("agr.*").ColumnExpr("oi.display_name AS agent_name").
		ColumnExpr("? AS agent_personal_responsible_name", chatstate.PersonalResponsibleName("oi")).
		ColumnExpr("oi.avatar_file_id AS agent_avatar_file_id").
		ColumnExpr(runHasProcessCondition+" AS has_process").
		Join("JOIN workspace_identities AS oi ON oi.id = agr.agent_identity_id AND oi.workspace_id = agr.workspace_id").
		Join("JOIN conversations AS c ON c.id = agr.conversation_id AND c.workspace_id = agr.workspace_id").
		Join("LEFT JOIN messages AS lm ON lm.id = c.last_message_id AND lm.workspace_id = c.workspace_id").
		Where("agr.workspace_id = ? AND agr.conversation_id = ?", workspaceID, conversationID).
		Where("agr.response_message_id IS NULL").
		Where("agr.status IN (?) OR (agr.status = ? AND (lm.created_at IS NULL OR agr.completed_at > lm.created_at))",
			bun.List(domain.AgentRunActiveStatuses),
			domain.AgentRunStatusCancelled).
		OrderExpr("agr.created_at, agr.id").Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load conversation agent runs: %w", err)
	}
	runs := make([]Run, 0, len(rows))
	for _, row := range rows {
		run := Run{ID: row.ID, AgentIdentityID: row.AgentIdentityID, AgentName: row.AgentName,
			AgentPersonalResponsibleName: row.AgentPersonalResponsibleName, AgentAvatarFileID: row.AgentAvatarFileID, Status: domain.AgentRunStatus(row.Status),
			ErrorCode: row.ErrorCode, LastError: row.LastError}
		// 已结束与挂起等待的运行给出已保存过程的引用。
		if row.HasProcess && row.StartedAt != nil && (row.CompletedAt != nil || row.Status == string(domain.AgentRunStatusWaiting)) {
			process, err := runReference(&row.AgentRun)
			if err != nil {
				return nil, err
			}
			run.Process = process
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// loadPendingAgents 按发言顺序读取已收到输入、等待轮转执行的 AI 员工。
func loadPendingAgents(ctx context.Context, db bun.IDB, workspaceID, conversationID string) ([]PendingAgent, error) {
	rows := make([]PendingAgent, 0)
	// 队列内输入序号连续，最早一条未处理输入即 processed_seq + 1；执行范围内至多一条活动运行，按其队列排除当前执行者。
	if err := db.NewSelect().TableExpr("agent_lanes AS al").
		ColumnExpr("al.agent_identity_id AS identity_id").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		ColumnExpr("? AS personal_responsible_name", chatstate.PersonalResponsibleName("oi")).
		Join("JOIN workspace_identities AS oi ON oi.id = al.agent_identity_id AND oi.workspace_id = al.workspace_id").
		Join("JOIN agent_inputs AS ai ON ai.lane_id = al.id AND ai.input_seq = al.processed_seq + 1").
		Join("JOIN messages AS msg ON msg.id = ai.source_message_id AND msg.workspace_id = ai.workspace_id").
		Where("al.workspace_id = ?", workspaceID).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeConversation, conversationID).
		Where("al.desired_seq > al.processed_seq").
		Where(`al.id IS DISTINCT FROM (
			SELECT agr.lane_id FROM agent_runs AS agr
			WHERE agr.workspace_id = ? AND agr.scope_kind = ? AND agr.scope_id = ? AND agr.status IN (?)
		)`, workspaceID, domain.AgentExecutionScopeConversation, conversationID,
			bun.List(domain.AgentRunActiveStatuses)).
		OrderExpr("msg.message_seq ASC, ai.source_ordinal ASC, al.id ASC").
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load conversation pending agents: %w", err)
	}
	return rows, nil
}
