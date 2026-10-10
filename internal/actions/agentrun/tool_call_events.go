//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// toolCallEventRow 是一条工具调用结果事件及其调用的当前内容。
type toolCallEventRow struct {
	ID                 string  `bun:"id"`
	MessageSeq         int64   `bun:"message_seq"`
	Name               string  `bun:"name"`
	BusinessSystemName *string `bun:"business_system_name"`
	Arguments          string  `bun:"arguments"`
	Status             string  `bun:"status"`
	Result             *string `bun:"result"`
	Error              *string `bun:"error"`
	DecidedBy          *string `bun:"decided_by"`
}

// mergeToolCallEvents 把会话中提交给本 AI 员工的工具调用结果事件按消息顺序并入上下文：只并入不越过 throughSeq 的事件，
// 上下文已截到历史上限时只并入窗口内的事件，服务周期执行范围只并入本周期的事件。
func mergeToolCallEvents(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, messages []agentcontract.Message, throughSeq int64) ([]agentcontract.Message, error) {
	ids := arr.FilterMap(messages, func(message agentcontract.Message) (string, bool) { return message.ID, str.IsUUID(message.ID) })
	type sequenced struct {
		ID         string `bun:"id"`
		MessageSeq int64  `bun:"message_seq"`
	}
	seqs := make(map[string]int64, len(ids))
	if len(ids) > 0 {
		var rows []sequenced
		if err := db.NewSelect().TableExpr("messages AS msg").ColumnExpr("msg.id, msg.message_seq").
			Where("msg.workspace_id = ? AND msg.conversation_id = ? AND msg.id IN (?)", run.WorkspaceID, run.ConversationID, bun.List(ids)).
			Scan(ctx, &rows); err != nil {
			return nil, fmt.Errorf("load context message sequences: %w", err)
		}
		for _, row := range rows {
			seqs[row.ID] = row.MessageSeq
		}
	}
	var lower int64
	if len(messages) >= agentHistoryLimit {
		lower = seqs[messages[0].ID]
	}
	query := db.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.id, msg.message_seq, atc.name, atc.business_system_name, atc.arguments, atc.status, atc.result, atc.error").
		ColumnExpr(agentprocess.SubjectName("atc.decided_by_subject_id")+" AS decided_by").
		Join("JOIN agent_tool_calls AS atc ON atc.workspace_id = msg.workspace_id AND atc.id = (msg.system_event_payload->>'toolCallId')::uuid").
		Join("JOIN agent_runs AS agr ON agr.id = atc.agent_run_id AND agr.workspace_id = atc.workspace_id").
		Where("msg.workspace_id = ? AND msg.conversation_id = ?", run.WorkspaceID, run.ConversationID).
		Where("msg.type = ? AND msg.system_event_type = ?", domain.MessageTypeSystem, domain.ConversationSystemEventAgentToolCallResolved).
		Where("msg.deleted_at IS NULL AND msg.message_seq > ? AND msg.message_seq <= ?", lower, throughSeq).
		Where("agr.agent_identity_id = ?", run.AgentIdentityID).
		OrderExpr("msg.message_seq ASC")
	if domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		query = query.Where("msg.service_session_id = ?", run.ScopeID)
	}
	var events []toolCallEventRow
	if err := query.Scan(ctx, &events); err != nil {
		return nil, fmt.Errorf("load tool call events for context: %w", err)
	}
	if len(events) == 0 {
		return messages, nil
	}
	merged := make([]agentcontract.Message, 0, len(messages)+len(events))
	for _, message := range messages {
		// 先放入早于该消息的事件；不在消息表中的合成消息沿用前一条消息的位置。
		seq, ok := seqs[message.ID]
		for ok && len(events) > 0 && events[0].MessageSeq < seq {
			merged = append(merged, events[0].message())
			events = events[1:]
		}
		merged = append(merged, message)
	}
	for _, event := range events {
		merged = append(merged, event.message())
	}
	return merged, nil
}

// message 把工具调用结果事件转换为交给模型的系统事件消息；已核对的调用仍按待核对说明。
func (r toolCallEventRow) message() agentcontract.Message {
	status := domain.AgentToolCallStatus(r.Status)
	if status == domain.AgentToolCallReviewed {
		status = domain.AgentToolCallNeedsReview
	}
	outcome := agentruntime.ToolCallOutcome{
		Tool: r.Name, Arguments: json.RawMessage(r.Arguments), Status: status, Result: r.Result, Error: r.Error,
		BusinessSystem: support.Deref(r.BusinessSystemName), DecidedBy: support.Deref(r.DecidedBy),
	}
	return agentcontract.Message{ID: r.ID, Role: agentcontract.MessageRoleUser, Content: outcome.Message(), Fact: true}
}
