//go:build server

// Package usernotification 为新消息、客服处理周期提醒与工具调用确认生成用户通知：按提醒口径与通知偏好确定接收成员，按接收人语言生成标题与正文，下发到本人的事件流，并经 control 推送到本人已登录的移动设备。
package usernotification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	"github.com/runforyou-ai/luway/internal/actions/inbox"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// Deliverer 执行新消息、客服处理周期提醒与工具调用确认的用户通知任务。
type Deliverer struct {
	db       *bun.DB
	inbox    *inbox.LoadInboxQuery
	enqueuer servertask.TxEnqueuer
}

// NewDeliverer 创建用户通知任务执行器，enqueuer 登记离线推送任务。
func NewDeliverer(db *bun.DB, enqueuer servertask.TxEnqueuer) *Deliverer {
	return &Deliverer{db: db, inbox: inbox.NewLoadInboxQuery(db), enqueuer: enqueuer}
}

// recipient 是开启消息通知且正在工作的有效成员，及其账号、账号语言和账号所在的有效工作区数。
type recipient struct {
	WorkspaceID    string `bun:"workspace_id"`
	WorkspaceName  string `bun:"workspace_name"`
	UserID         string `bun:"user_id"`
	IdentityID     string `bun:"identity_id"`
	AccountID      string `bun:"account_id"`
	Locale         string `bun:"locale"`
	WorkspaceCount int    `bun:"workspace_count"`
}

// delivery 是发给一个接收人的用户通知。
type delivery struct {
	recipient
	notification realtime.Notification
}

// identity 返回按该成员身份读取收件箱所需的成员身份。
func (r recipient) identity() *servermodels.Identity {
	return &servermodels.Identity{
		Workspace:         servermodels.Workspace{ID: r.WorkspaceID},
		WorkspaceIdentity: servermodels.WorkspaceIdentity{ID: r.IdentityID},
		User:              servermodels.User{ID: r.UserID},
	}
}

// viewer 返回按该成员读取收件箱的查看者。
func (r recipient) viewer() inbox.Viewer {
	return inbox.Viewer{WorkspaceID: r.WorkspaceID, IdentityID: r.IdentityID, UserID: r.UserID}
}

// workspaceTitle 给加入多个工作区的接收人在标题前标明工作区。
func (r recipient) workspaceTitle(title string) string {
	if r.WorkspaceCount <= 1 {
		return title
	}
	return localize(r.Locale, i18n.NotificationWorkspaceTitle, map[string]any{"Workspace": r.WorkspaceName, "Title": title})
}

// recipientsQuery 构造工作区中可接收通知的成员查询：成员与账号有效、工作区正常、开启消息通知且工作状态为工作中。
func recipientsQuery(db bun.IDB, workspaceID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("users AS u").
		Join("JOIN workspace_identities AS oi ON oi.workspace_id = u.workspace_id AND oi.id = u.identity_id").
		Join("JOIN workspaces AS o ON o.id = u.workspace_id").
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		ColumnExpr("u.workspace_id::text AS workspace_id, o.name AS workspace_name, u.id::text AS user_id, oi.id::text AS identity_id, acc.id::text AS account_id, acc.locale").
		ColumnExpr(`(SELECT count(*) FROM users AS mine JOIN workspaces AS mine_o ON mine_o.id = mine.workspace_id
			WHERE mine.account_id = acc.id AND mine.status = ? AND mine_o.lifecycle_status = ?) AS workspace_count`,
			domain.IdentityStatusActive, domain.WorkspaceLifecycleActive).
		Where("u.workspace_id = ? AND u.status = ? AND u.message_notifications_enabled", workspaceID, domain.IdentityStatusActive).
		Where("oi.type = ? AND oi.work_status = ?", domain.WorkspaceIdentityTypeUser, domain.WorkStatusWorking).
		Where("acc.status = ? AND o.lifecycle_status = ?", domain.AccountStatusActive, domain.WorkspaceLifecycleActive)
}

// DeliverMessage 为一条新消息生成用户通知：候选成员为可在收件箱中展示该会话的成员，按各自提醒口径批量判断该消息仍是计入提醒的未读消息后下发。
func (d *Deliverer) DeliverMessage(ctx context.Context, input notificationtask.MessageInput) error {
	var recipients []recipient
	if err := recipientsQuery(d.db, input.WorkspaceID).
		Where("EXISTS (SELECT 1 FROM conversations AS cv WHERE cv.workspace_id = u.workspace_id AND cv.id = ? AND ?)",
			input.ConversationID, conversationaccess.Listable(conversationaccess.Viewer{WorkspaceID: bun.Safe("u.workspace_id"), IdentityID: bun.Safe("oi.id")}, "cv")).
		Scan(ctx, &recipients); err != nil {
		return fmt.Errorf("load message notification recipients: %w", err)
	}
	attentions, err := d.inbox.ReadMessageAttentions(ctx, arr.Map(recipients, recipient.viewer), input.ConversationID, input.MessageID)
	if err != nil {
		return fmt.Errorf("read message attention: %w", err)
	}
	deliveries := make([]delivery, 0, len(recipients))
	for index, target := range recipients {
		attention := attentions[index]
		if attention == nil || len(attention.Messages) == 0 {
			continue
		}
		summary := attention.Conversation
		deliveries = append(deliveries, delivery{recipient: target, notification: realtime.UserNotificationFor(
			target.WorkspaceID, target.UserID, "message:"+input.MessageID, input.ConversationID, notificationView(summary),
			target.workspaceTitle(conversationTitle(target.Locale, summary)), messageBody(target.Locale, summary, attention.Messages[0]),
		)})
	}
	return d.dispatch(ctx, deliveries)
}

// DeliverServiceAttention 为一次客服处理周期提醒生成用户通知，成员已不可接收通知或已无法阅读该会话时不下发。
func (d *Deliverer) DeliverServiceAttention(ctx context.Context, input notificationtask.ServiceAttentionInput) error {
	var recipients []recipient
	if err := recipientsQuery(d.db, input.WorkspaceID).Where("u.id = ?", input.UserID).Scan(ctx, &recipients); err != nil {
		return fmt.Errorf("load service attention notification recipient: %w", err)
	}
	if len(recipients) == 0 {
		return nil
	}
	target := recipients[0]
	results, err := d.inbox.ReadByIDs(ctx, target.identity(), []string{input.ConversationID}, nil)
	if err != nil {
		return fmt.Errorf("read service attention conversation: %w", err)
	}
	summary := results[0].Conversation
	if summary == nil {
		return nil
	}
	return d.dispatch(ctx, []delivery{{recipient: target, notification: realtime.UserNotificationFor(
		target.WorkspaceID, target.UserID, input.NotificationID, input.ConversationID, notificationView(*summary),
		target.workspaceTitle(conversationTitle(target.Locale, *summary)), localize(target.Locale, serviceAttentionBodies[input.Reason], nil),
	)}})
}

// dispatch 授权含推送中继时先为接收人按应用与平台登记离线推送任务，再把用户通知下发到各自的事件流。
func (d *Deliverer) dispatch(ctx context.Context, deliveries []delivery) error {
	if len(deliveries) == 0 {
		return nil
	}
	capabilities, err := licenseaction.Capabilities(ctx, d.db)
	if err != nil {
		return fmt.Errorf("load platform capabilities: %w", err)
	}
	if capabilities.PushRelay {
		if err := d.enqueuePushes(ctx, deliveries); err != nil {
			return err
		}
	}
	realtime.Publish(arr.Map(deliveries, func(item delivery) realtime.Notification { return item.notification })...)
	return nil
}

// enqueuePushes 读取接收账号未过期登录会话上的推送设备，每个接收人在同一应用与平台上的设备登记为一个离线推送任务。
func (d *Deliverer) enqueuePushes(ctx context.Context, deliveries []delivery) error {
	devices, err := loadPushDevices(ctx, d.db, arr.Map(deliveries, func(item delivery) string { return item.AccountID }))
	if err != nil {
		return err
	}
	var pushes []servertask.EnqueueRequest
	for _, item := range deliveries {
		for _, group := range devices[item.AccountID] {
			pushes = append(pushes, notificationtask.PushRequest(notificationtask.PushInput{
				AccountID: item.AccountID, UserID: item.UserID, App: group.app, Platform: group.platform, DeviceIDs: group.deviceIDs,
				WorkspaceID: item.notification.WorkspaceID, NotificationID: item.notification.NotificationID,
				ConversationID: item.notification.ConversationID, View: item.notification.View, Title: item.notification.Title, Body: item.notification.Body,
			}))
		}
	}
	if len(pushes) == 0 {
		return nil
	}
	if err := serverstorage.RunInTx(ctx, d.db, func(ctx context.Context, tx bun.Tx) error {
		err := d.enqueuer.EnqueueManyIn(ctx, pushes)
		return err
	}); err != nil {
		return fmt.Errorf("enqueue push notifications: %w", err)
	}
	return nil
}

// DeliverToolDecision 通知处理成员确认或审批 AI 员工提交的工具调用，点击后打开待处理列表；调用已处理（含暂停运行的调用已记下决定）或成员已不可接收通知时不下发。
func (d *Deliverer) DeliverToolDecision(ctx context.Context, input notificationtask.ToolDecisionInput) error {
	var recipients []recipient
	if err := recipientsQuery(d.db, input.WorkspaceID).Where("u.id = ?", input.UserID).Scan(ctx, &recipients); err != nil {
		return fmt.Errorf("load tool decision notification recipient: %w", err)
	}
	if len(recipients) == 0 {
		return nil
	}
	var call struct {
		Name         string `bun:"name"`
		Intervention string `bun:"intervention"`
		AgentName    string `bun:"agent_name"`
	}
	err := d.db.NewSelect().TableExpr("agent_tool_calls AS atc").
		ColumnExpr("atc.name, atc.intervention, oi.display_name AS agent_name").
		Join("JOIN agent_runs AS agr ON agr.id = atc.agent_run_id AND agr.workspace_id = atc.workspace_id").
		Join("JOIN workspace_identities AS oi ON oi.id = agr.agent_identity_id AND oi.workspace_id = agr.workspace_id").
		Where("atc.workspace_id = ? AND atc.id = ? AND atc.status = ? AND atc.decision IS NULL", input.WorkspaceID, input.ToolCallID, domain.AgentToolCallAwaitingDecision).
		Scan(ctx, &call)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load tool decision notification call: %w", err)
	}
	target := recipients[0]
	body := toolDecisionBodies[domain.ToolIntervention(call.Intervention)]
	return d.dispatch(ctx, []delivery{{recipient: target, notification: realtime.UserNotificationFor(
		target.WorkspaceID, target.UserID, "tool_decision:"+input.ToolCallID, "", domain.NotificationViewPending,
		target.workspaceTitle(localize(target.Locale, i18n.NotificationToolDecisionTitle, nil)), localize(target.Locale, body, map[string]any{"Agent": call.AgentName, "Tool": call.Name}),
	)}})
}
