//go:build server

package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadConversationAgentProcesses 在已授权的消息窗口中批量补充已完成运行的过程引用、尚未由消息表达的运行状态和等待发言的 AI 员工。
func loadConversationAgentProcesses(ctx context.Context, db bun.IDB, organizationID, conversationID string, history *ConversationMessageHistory) error {
	if err := loadConversationAgentRuns(ctx, db, organizationID, conversationID, history); err != nil {
		return err
	}
	if err := loadConversationPendingAgents(ctx, db, organizationID, conversationID, history); err != nil {
		return err
	}
	if len(history.Messages) == 0 {
		return nil
	}
	messagePositions := make(map[string]int, len(history.Messages))
	messageIDs := make([]string, 0, len(history.Messages))
	for i, message := range history.Messages {
		messagePositions[message.ID] = i
		messageIDs = append(messageIDs, message.ID)
	}
	var runs []servermodels.AgentRun
	if err := db.NewSelect().Model(&runs).
		Where("agr.organization_id = ? AND agr.conversation_id = ?", organizationID, conversationID).
		Where("agr.response_message_id IN (?)", bun.In(messageIDs)).
		Where("agr.started_at IS NOT NULL AND agr.completed_at IS NOT NULL").
		Where(agentRunHasProcessCondition).Scan(ctx); err != nil {
		return fmt.Errorf("load message agent processes: %w", err)
	}
	for _, run := range runs {
		process, err := conversationAgentProcess(&run)
		if err != nil {
			return err
		}
		history.Messages[messagePositions[*run.ResponseMessageID]].AgentProcess = process
	}
	// 为 AI 回复失败消息补充对应运行的稳定失败原因。
	var failures []struct {
		ResponseMessageID string                   `bun:"response_message_id"`
		ErrorCode         domain.AgentRunErrorCode `bun:"error_code"`
	}
	if err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Column("response_message_id", "error_code").
		Where("agr.organization_id = ? AND agr.conversation_id = ?", organizationID, conversationID).
		Where("agr.response_message_id IN (?)", bun.In(messageIDs)).
		Where("agr.status = ? AND agr.error_code IS NOT NULL", domain.AgentRunStatusFailed).
		Scan(ctx, &failures); err != nil {
		return fmt.Errorf("load message agent failures: %w", err)
	}
	for _, failure := range failures {
		history.Messages[messagePositions[failure.ResponseMessageID]].AgentErrorCode = &failure.ErrorCode
	}
	return nil
}

// agentRunHasProcessCondition 限定已持久化过程内容的运行，没有内容的运行不给出可展开的过程引用。
const agentRunHasProcessCondition = `EXISTS (
	SELECT 1 FROM agent_run_blocks AS arb
	WHERE arb.organization_id = agr.organization_id AND arb.agent_run_id = agr.id
)`

// runDuration 返回运行从开始到结束的时长，挂起等待的运行计到最近一次更新。
func runDuration(run *servermodels.AgentRun) time.Duration {
	end := run.UpdatedAt
	if run.CompletedAt != nil {
		end = *run.CompletedAt
	}
	return end.Sub(*run.StartedAt)
}

// conversationAgentProcess 按运行的起止时间、模型用量和结果构造过程引用。
func conversationAgentProcess(run *servermodels.AgentRun) (*ConversationAgentProcess, error) {
	process := &ConversationAgentProcess{ID: run.ID, DurationMilliseconds: runDuration(run).Milliseconds(),
		Outcome: (*domain.AgentRunOutcome)(run.Outcome), OutcomeReason: (*domain.AgentHandoffReason)(run.OutcomeReason)}
	if err := json.Unmarshal(run.Usage, &process.Usage); err != nil {
		return nil, fmt.Errorf("decode agent usage: %w", err)
	}
	return process, nil
}

// loadConversationAgentRuns 读取尚未由结果消息表达的运行：仍在执行的运行，以及结束后会话再无新消息的取消运行。取消时间与消息创建时间同取数据库时钟。
func loadConversationAgentRuns(ctx context.Context, db bun.IDB, organizationID, conversationID string, history *ConversationMessageHistory) error {
	var rows []struct {
		servermodels.AgentRun        `bun:",embed"`
		AgentName                    string  `bun:"agent_name"`
		AgentPersonalResponsibleName *string `bun:"agent_personal_responsible_name"`
		AgentAvatarFileID            *string `bun:"agent_avatar_file_id"`
		ExecutionDeviceName          *string `bun:"execution_device_name"`
		HasProcess                   bool    `bun:"has_process"`
	}
	if err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		ColumnExpr("agr.*").ColumnExpr("oi.display_name AS agent_name").
		ColumnExpr("? AS agent_personal_responsible_name", PersonalResponsibleName("oi")).
		ColumnExpr("oi.avatar_file_id AS agent_avatar_file_id").
		ColumnExpr(agentRunHasProcessCondition+" AS has_process").
		ColumnExpr("d.name AS execution_device_name").
		Join("JOIN organization_identities AS oi ON oi.id = agr.agent_identity_id AND oi.organization_id = agr.organization_id").
		Join("LEFT JOIN devices AS d ON d.id = agr.execution_device_id AND d.organization_id = agr.organization_id").
		Join("JOIN conversations AS c ON c.id = agr.conversation_id AND c.organization_id = agr.organization_id").
		Join("LEFT JOIN messages AS lm ON lm.id = c.last_message_id AND lm.organization_id = c.organization_id").
		Where("agr.organization_id = ? AND agr.conversation_id = ?", organizationID, conversationID).
		Where("agr.response_message_id IS NULL").
		Where("agr.status IN (?) OR (agr.status = ? AND (lm.created_at IS NULL OR agr.completed_at > lm.created_at))",
			bun.In(domain.AgentRunActiveStatuses),
			domain.AgentRunStatusCancelled).
		OrderExpr("agr.created_at, agr.id").Scan(ctx, &rows); err != nil {
		return fmt.Errorf("load conversation agent runs: %w", err)
	}
	history.AgentRuns = make([]ConversationAgentRun, 0, len(rows))
	for _, row := range rows {
		run := ConversationAgentRun{ID: row.ID, AgentIdentityID: row.AgentIdentityID, AgentName: row.AgentName,
			AgentPersonalResponsibleName: row.AgentPersonalResponsibleName, AgentAvatarFileID: row.AgentAvatarFileID, Status: domain.AgentRunStatus(row.Status),
			ErrorCode: row.ErrorCode, LastError: row.LastError, ExecutionDeviceID: row.ExecutionDeviceID, ExecutionDeviceName: row.ExecutionDeviceName}
		// 已结束与挂起等待的运行给出已保存过程的引用。
		if row.HasProcess && row.StartedAt != nil && (row.CompletedAt != nil || row.Status == string(domain.AgentRunStatusWaiting)) {
			process, err := conversationAgentProcess(&row.AgentRun)
			if err != nil {
				return err
			}
			run.Process = process
		}
		history.AgentRuns = append(history.AgentRuns, run)
	}
	return nil
}

// loadConversationPendingAgents 按发言顺序读取已收到输入、等待轮转执行的 AI 员工。
func loadConversationPendingAgents(ctx context.Context, db bun.IDB, organizationID, conversationID string, history *ConversationMessageHistory) error {
	rows := make([]ConversationPendingAgent, 0)
	// 队列内输入序号连续，最早一条未处理输入即 processed_seq + 1；执行范围内至多一条活动运行，按其队列排除当前执行者。
	if err := db.NewSelect().TableExpr("agent_lanes AS al").
		ColumnExpr("al.agent_identity_id AS identity_id").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		ColumnExpr("? AS personal_responsible_name", PersonalResponsibleName("oi")).
		Join("JOIN organization_identities AS oi ON oi.id = al.agent_identity_id AND oi.organization_id = al.organization_id").
		Join("JOIN agent_inputs AS ai ON ai.lane_id = al.id AND ai.input_seq = al.processed_seq + 1").
		Join("JOIN messages AS msg ON msg.id = ai.source_message_id AND msg.organization_id = ai.organization_id").
		Where("al.organization_id = ?", organizationID).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeConversation, conversationID).
		Where("al.desired_seq > al.processed_seq").
		Where(`al.id IS DISTINCT FROM (
			SELECT agr.lane_id FROM agent_runs AS agr
			WHERE agr.organization_id = ? AND agr.scope_kind = ? AND agr.scope_id = ? AND agr.status IN (?)
		)`, organizationID, domain.AgentExecutionScopeConversation, conversationID,
			bun.In(domain.AgentRunActiveStatuses)).
		OrderExpr("msg.message_seq ASC, ai.source_ordinal ASC, al.id ASC").
		Scan(ctx, &rows); err != nil {
		return fmt.Errorf("load conversation pending agents: %w", err)
	}
	history.PendingAgents = rows
	return nil
}
