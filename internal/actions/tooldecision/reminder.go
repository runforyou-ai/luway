//go:build server

package tooldecision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// ToolDecisionReminderActionName 是经外部平台渠道提醒发起成员确认工具调用的任务 Action 名称。
const ToolDecisionReminderActionName = "agent.tool_call.channel_reminder"

// enqueueToolDecisionReminder 在提交确认的事务中为渠道会话投递确认提醒任务，同一调用只提醒一次。
func enqueueToolDecisionReminder(ctx context.Context, tx bun.Tx, enqueuer servertask.TxEnqueuer, workspaceID, callID string) error {
	if _, err := enqueuer.EnqueueIn(ctx, ToolDecisionReminderActionName, agentprocess.ToolCallInput{WorkspaceID: workspaceID, ToolCallID: callID}, servertask.EnqueueOptions{
		WorkspaceID: workspaceID, MaxAttempts: 3, IdempotencyKey: "agent-tool-call-reminder:" + callID,
	}); err != nil {
		return fmt.Errorf("enqueue agent tool call reminder: %w", err)
	}
	return nil
}

// ReminderAction 在经外部平台投递的渠道会话中，以 AI 员工身份向发起成员发送带处理页面链接的确认提醒。
type ReminderAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	publicURL func() string
}

// NewReminderAction 创建确认提醒任务，publicURL 返回生成处理页面链接的部署地址。
func NewReminderAction(db *bun.DB, enqueuer servertask.TxEnqueuer, publicURL func() string) *ReminderAction {
	return &ReminderAction{db: db, enqueuer: enqueuer, publicURL: publicURL}
}

// ToolDecisionLink 返回部署地址下处理指定工具调用的页面链接。
func ToolDecisionLink(publicURL, workspaceSlug, toolCallID string) string {
	return strings.TrimRight(publicURL, "/") + domain.WebAppPath + "#/w/" + workspaceSlug + "/pending?decision=" + toolCallID
}

// Execute 在调用仍等待发起成员确认且未过截止时间、所在服务周期仍进行中、渠道可以向发起成员投递时写入提醒消息并安排投递；其余情况不提醒。
func (a *ReminderAction) Execute(ctx context.Context, input agentprocess.ToolCallInput) error {
	var call struct {
		Intervention *string `bun:"intervention"`
		Status       string  `bun:"status"`
		Conversation string  `bun:"conversation_id"`
		Session      string  `bun:"scope_id"`
		ScopeKind    string  `bun:"scope_kind"`
		AgentID      string  `bun:"agent_identity_id"`
		Workspace    string  `bun:"slug"`
	}
	err := a.db.NewSelect().TableExpr("agent_tool_calls AS atc").
		ColumnExpr("atc.intervention, atc.status, ar.conversation_id, ar.scope_id, ar.scope_kind, ar.agent_identity_id, o.slug").
		Join("JOIN agent_runs AS ar ON ar.id = atc.agent_run_id AND ar.workspace_id = atc.workspace_id").
		Join("JOIN workspaces AS o ON o.id = atc.workspace_id").
		Where("atc.id = ? AND atc.workspace_id = ?", input.ToolCallID, input.WorkspaceID).
		Scan(ctx, &call)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load agent tool call for reminder: %w", err)
	}
	if call.Intervention == nil || domain.ToolIntervention(*call.Intervention) != domain.ToolInterventionConfirmation ||
		domain.AgentToolCallStatus(call.Status) != domain.AgentToolCallAwaitingDecision ||
		domain.AgentExecutionScopeKind(call.ScopeKind) != domain.AgentExecutionScopeServiceSession {
		return nil
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 外部平台渠道先锁渠道和渠道身份，再锁会话与服务周期。
		route, err := deliveryaction.Prepare(ctx, tx, input.WorkspaceID, call.Conversation)
		if errors.Is(err, deliveryaction.ErrUnavailable) {
			return nil
		}
		if err != nil {
			return err
		}
		if !route.Platform() || !route.Ready() {
			return nil
		}
		locked, err := chatstate.LockServiceSession(ctx, tx, input.WorkspaceID, call.Conversation)
		if err != nil {
			return err
		}
		session := locked.Session
		if session.ID != call.Session || domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen {
			return nil
		}
		// 取得锁后按事务内的当前值与当前时刻复核调用仍在等待确认且未过截止时间。
		pending, err := tx.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
			Where("id = ? AND workspace_id = ? AND status = ? AND expires_at > clock_timestamp()", input.ToolCallID, input.WorkspaceID, domain.AgentToolCallAwaitingDecision).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("recheck agent tool call for reminder: %w", err)
		}
		if !pending {
			return nil
		}
		channel, err := serviceroute.LoadConversationChannel(ctx, tx, input.WorkspaceID, call.Conversation)
		if err != nil {
			return err
		}
		var agentName string
		if err := tx.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).Column("display_name").
			Where("oi.workspace_id = ? AND oi.id = ?", input.WorkspaceID, call.AgentID).Scan(ctx, &agentName); err != nil {
			return fmt.Errorf("load agent name for tool call reminder: %w", err)
		}
		participantID, err := agentmessage.EnsureCustomerParticipant(ctx, tx, input.WorkspaceID, call.Conversation, call.AgentID)
		if err != nil {
			return err
		}
		body := i18n.LocalizeCustomerTemplate(domain.CustomerLocale(channel.DefaultLocale), i18n.EmployeeChannelDecisionReminder, map[string]any{
			"Agent": agentName, "Link": ToolDecisionLink(a.publicURL(), call.Workspace, input.ToolCallID),
		})
		if _, err := agentmessage.AppendCustomer(ctx, tx, a.enqueuer, locked.Conversation, session, route, &servermodels.Message{
			ID: uuid.NewV7().String(), WorkspaceID: input.WorkspaceID, ConversationID: call.Conversation,
			ServiceSessionID: &session.ID, SenderParticipantID: &participantID,
			Type: string(domain.MessageTypeText), Body: body, IdempotencyKey: new("agent_tool_call_reminder:" + input.ToolCallID),
		}); err != nil {
			return fmt.Errorf("append agent tool call reminder: %w", err)
		}
		return nil
	})
}
