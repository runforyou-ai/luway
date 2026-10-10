//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	modelprovider "github.com/runforyou-ai/einorun/provider"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	channelbindingaction "github.com/runforyou-ai/luway/internal/actions/channelbinding"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// recordedSend 是测试适配器收到的一次发送。
type recordedSend struct {
	target channeladapter.Target
	body   string
}

// recordingOutbound 记录发送请求并返回 outcome，未设置时返回已发送。
type recordingOutbound struct {
	mu      sync.Mutex
	sends   []recordedSend
	outcome channeladapter.Outcome
}

// Plan 把消息作为一个请求项。
func (o *recordingOutbound) Plan(body string, attachment bool) []channeladapter.Item {
	return []channeladapter.Item{{Body: body, Attachment: attachment}}
}

// SendTimeout 返回固定超时。
func (o *recordingOutbound) SendTimeout(channeladapter.Item) time.Duration { return 5 * time.Second }

// Send 记录请求。
func (o *recordingOutbound) Send(_ context.Context, target channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sends = append(o.sends, recordedSend{target: target, body: request.Body})
	if o.outcome != "" {
		return channeladapter.Result{Outcome: o.outcome}
	}
	return channeladapter.Result{Outcome: channeladapter.OutcomeSent}
}

// take 返回并清空已记录的发送。
func (o *recordingOutbound) take() []recordedSend {
	o.mu.Lock()
	defer o.mu.Unlock()
	sends := o.sends
	o.sends = nil
	return sends
}

// employeeChannelFixture 是员工渠道的测试环境。
type employeeChannelFixture struct {
	db       *bun.DB
	identity *models.Identity
	modelID  string
	agentID  string
	channel  *channelaction.MessageChannelRecord
	botID    string
	outbound *recordingOutbound
	receiver *channelinboundaction.ReceiveEventAction
	tasks    *servertest.Tasks
}

// newEmployeeChannelFixture 创建新会话交给服务员工的 AI 员工、已保存机器人凭据的企业微信机器人渠道。
func newEmployeeChannelFixture(t *testing.T) *employeeChannelFixture {
	t.Helper()
	ctx := context.Background()
	db, identity, _, modelID := newAIWorkspace(t)
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee}, DisplayName: "IT 助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "回答同事问题"}},
	})
	require.NoError(t, err)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWeComBot, Name: "企业微信 IT 助手", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	botID := "bot-" + uuid.NewV7().String()
	_, err = channelaction.NewSaveWeComBotConnectionAction(db).Execute(ctx, identity, channel.ID, channelaction.WeComBotConnectionInput{BotID: botID, Secret: "secret"})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	outbound := &recordingOutbound{}
	adapters := channeladapter.NewRegistry()
	adapters.Register(domain.ChannelTypeWeComBot, outbound)
	return &employeeChannelFixture{
		db: db, identity: identity, modelID: modelID, agentID: agent.IdentityID, channel: channel, botID: botID, outbound: outbound, tasks: tasks,
		receiver: channelinboundaction.NewReceiveEventAction(db, adapters, agentrunaction.NewScheduler(tasks), localStorage, tasks, nil, func() string { return "https://luway.test" }),
	}
}

// receive 处理一条入站事件；id 为空时生成新的事件编号。
func (f *employeeChannelFixture) receive(t *testing.T, event channeladapter.InboundEvent) {
	t.Helper()
	if event.ID == "" {
		event.ID = "msg-" + uuid.NewV7().String()
	}
	if event.ChatID == "" {
		event.ChatID = event.Sender
	}
	if event.Kind == "" {
		event.Kind = channeladapter.InboundEventMessage
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	require.NoError(t, f.receiver.Execute(context.Background(), channelinboundaction.ReceiveEventInput{
		WorkspaceID: f.identity.Workspace.ID, ChannelID: f.channel.ID, AccountID: f.botID, Event: event,
	}))
}

// requesterConversations 返回外部账号的渠道会话及其发起人与服务对象。
func (f *employeeChannelFixture) requesterConversations(t *testing.T, externalID string) []struct {
	ConversationID string `bun:"conversation_id"`
	Audience       string `bun:"audience"`
	Kind           string `bun:"kind"`
	SourceID       string `bun:"source_id"`
} {
	t.Helper()
	var rows []struct {
		ConversationID string `bun:"conversation_id"`
		Audience       string `bun:"audience"`
		Kind           string `bun:"kind"`
		SourceID       string `bun:"source_id"`
	}
	require.NoError(t, f.db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cc.conversation_id::text, svc.audience, cs.kind, cs.source_id::text").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").
		Join("JOIN service_conversations AS svc ON svc.conversation_id = cc.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.id = svc.requester_subject_id").
		Where("ci.channel_id = ? AND ci.external_id = ?", f.channel.ID, externalID).
		OrderExpr("cc.created_at").
		Scan(context.Background(), &rows))
	return rows
}

var bindingTokenPattern = regexp.MustCompile(`#/channel-bindings/([A-Za-z0-9_-]+)`)

// TestEmployeeChannelBinding 验证未绑定账号收到限频的绑定链接，成员确认后私聊进入以成员为发起人、服务对象为员工的会话并交给 AI 员工，改绑后开启新会话。
func TestEmployeeChannelBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)

	// 进入会话不签发链接，平台只允许向发过消息的对方主动发送。
	f.receive(t, channeladapter.InboundEvent{Kind: channeladapter.InboundEventEntered, Sender: "zhangsan"})
	require.Empty(t, f.outbound.take(), "进入会话不应发送")
	f.receive(t, channeladapter.InboundEvent{Sender: "zhangsan", Text: "VPN 连不上"})
	sends := f.outbound.take()
	require.Len(t, sends, 1)
	require.Equal(t, "zhangsan", sends[0].target.Recipient)
	require.Equal(t, f.botID, sends[0].target.AccountID)
	match := bindingTokenPattern.FindStringSubmatch(sends[0].body)
	require.NotNil(t, match, "binding link missing: %q", sends[0].body)
	require.Empty(t, f.requesterConversations(t, "zhangsan"), "未绑定账号不应进入会话")
	// 发送间隔内再次发消息不重复发送绑定链接。
	f.receive(t, channeladapter.InboundEvent{Sender: "zhangsan", Text: "还在吗"})
	require.Empty(t, f.outbound.take(), "发送间隔内不应重复发送绑定链接")

	preview, err := channelbindingaction.NewPreviewQuery(f.db).Execute(ctx, match[1])
	require.NoError(t, err)
	require.Equal(t, domain.ChannelBindingStatusPending, preview.Status)
	require.Equal(t, domain.ChannelTypeWeComBot, preview.ChannelType)
	require.Equal(t, "zhangsan", preview.ExternalName)
	slug, err := channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, match[1])
	require.NoError(t, err)
	require.Equal(t, f.identity.Workspace.Slug, slug)
	_, err = channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, match[1])
	require.ErrorIs(t, err, channelbindingaction.ErrLinkInvalid, "重复使用的令牌应失效")

	event := channeladapter.InboundEvent{ID: "msg-bound-1", Sender: "zhangsan", Text: "VPN 连不上"}
	f.receive(t, event)
	f.receive(t, event)
	rows := f.requesterConversations(t, "zhangsan")
	require.Len(t, rows, 1)
	require.Equal(t, string(domain.ServiceAudienceEmployee), rows[0].Audience)
	require.Equal(t, string(domain.ChatSubjectKindWorkspaceIdentity), rows[0].Kind)
	require.Equal(t, f.identity.WorkspaceIdentity.ID, rows[0].SourceID)
	messages, err := f.db.NewSelect().TableExpr("messages").Where("conversation_id = ? AND body = ?", rows[0].ConversationID, "VPN 连不上").Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), messages, "重复事件只写入一条消息")
	runs, err := f.db.NewSelect().TableExpr("agent_runs").Where("conversation_id = ? AND agent_identity_id = ?", rows[0].ConversationID, f.agentID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), runs, "重复事件只调度一次 AI 员工")

	// 群聊提及回复提示且不写入会话；不支持的消息写入会话并投递一次提示任务，重复事件不再投递。
	f.receive(t, channeladapter.InboundEvent{Kind: channeladapter.InboundEventGroupMention, Sender: "zhangsan", ChatID: "group-1", Text: "@IT 助手"})
	sends = f.outbound.take()
	require.Len(t, sends, 1)
	require.Equal(t, "group-1", sends[0].target.Recipient)
	require.True(t, sends[0].target.Group)
	require.Equal(t, "请私聊使用。", sends[0].body)
	unsupported := channeladapter.InboundEvent{ID: "msg-unsupported-1", Sender: "zhangsan", Unsupported: true}
	f.receive(t, unsupported)
	f.receive(t, unsupported)
	unsupportedCount, err := f.db.NewSelect().TableExpr("messages").Where("conversation_id = ? AND type = ?", rows[0].ConversationID, domain.MessageTypeUnsupported).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), unsupportedCount)
	notices := queuedNotices(t, f.tasks, f.identity.Workspace.ID)
	require.Len(t, notices, 1)
	require.Equal(t, "zhangsan", notices[0].Recipient)

	// 管理员改绑到其他成员后，新消息进入以新成员为发起人的会话。
	other := newChatLockUser(t, f.db, f.identity)
	accounts, err := channelbindingaction.NewListAccountsQuery(f.db).Execute(ctx, f.identity, f.channel.ID)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.NotNil(t, accounts[0].MemberIdentityID)
	require.Equal(t, f.identity.WorkspaceIdentity.ID, *accounts[0].MemberIdentityID)
	manage := channelbindingaction.NewManageAction(f.db)
	require.NoError(t, manage.Bind(ctx, f.identity, f.channel.ID, accounts[0].ID, other.WorkspaceIdentity.ID))
	f.receive(t, channeladapter.InboundEvent{Sender: "zhangsan", Text: "换了电脑"})
	rows = f.requesterConversations(t, "zhangsan")
	require.Len(t, rows, 2)
	require.Equal(t, other.WorkspaceIdentity.ID, rows[1].SourceID)
	require.NotEqual(t, rows[0].ConversationID, rows[1].ConversationID, "改绑后应开启新会话")

	// 解除绑定后再次发消息重新收到绑定链接。
	require.NoError(t, manage.Unbind(ctx, f.identity, f.channel.ID, accounts[0].ID))
	f.receive(t, channeladapter.InboundEvent{Sender: "zhangsan", Text: "在吗"})
	sends = f.outbound.take()
	require.Len(t, sends, 1)
	require.Regexp(t, bindingTokenPattern, sends[0].body, "解除绑定后应重新收到绑定链接")
}

// TestEmployeeChannelBindingRequiresMember 验证非工作区成员不能确认绑定，同一成员在渠道中只能绑定一个外部账号。
func TestEmployeeChannelBindingRequiresMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)
	outsider, _, _ := newAIWorkspaceIn(t, f.db)

	link := func(sender string) string {
		f.receive(t, channeladapter.InboundEvent{Sender: sender, Text: "你好"})
		sends := f.outbound.take()
		require.Len(t, sends, 1)
		return bindingTokenPattern.FindStringSubmatch(sends[0].body)[1]
	}
	first := link("lisi")
	_, err := channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: outsider.Account}, first)
	require.ErrorIs(t, err, channelbindingaction.ErrNotMember, "非工作区成员不能确认绑定")
	_, err = channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, first)
	require.NoError(t, err)
	second := link("lisi-alt")
	_, err = channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, second)
	require.ErrorIs(t, err, channelbindingaction.ErrMemberBound, "同一成员在渠道中只能绑定一个外部账号")
}

// TestEmployeeChannelSkipsRequesterRoute 验证渠道初始目标是发起成员本人时，新周期不交给本人而进入队列。
func TestEmployeeChannelSkipsRequesterRoute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)
	_, err := f.db.NewUpdate().TableExpr("channels").Set("initial_routing_target_id = ?", f.identity.WorkspaceIdentity.ID).
		Where("id = ?", f.channel.ID).Exec(ctx)
	require.NoError(t, err)
	f.receive(t, channeladapter.InboundEvent{Sender: "wangwu", Text: "你好"})
	token := bindingTokenPattern.FindStringSubmatch(f.outbound.take()[0].body)[1]
	_, err = channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, token)
	require.NoError(t, err)
	f.receive(t, channeladapter.InboundEvent{Sender: "wangwu", Text: "打印机坏了"})
	rows := f.requesterConversations(t, "wangwu")
	require.Len(t, rows, 1)
	var assignee *string
	require.NoError(t, f.db.NewSelect().TableExpr("service_sessions").Column("assignee_identity_id").
		Where("conversation_id = ?", rows[0].ConversationID).Scan(ctx, &assignee))
	require.False(t, assignee != nil && *assignee == f.identity.WorkspaceIdentity.ID, "新周期不应交给发起成员本人")
}

// TestEmployeeChannelLinkDelivery 验证无法确认送达的绑定链接不计入发送间隔，确认送达后才限频。
func TestEmployeeChannelLinkDelivery(t *testing.T) {
	t.Parallel()
	f := newEmployeeChannelFixture(t)
	f.outbound.outcome = channeladapter.OutcomeUncertain
	f.receive(t, channeladapter.InboundEvent{Sender: "zhaoliu", Text: "你好"})
	f.outbound.outcome = ""
	f.receive(t, channeladapter.InboundEvent{Sender: "zhaoliu", Text: "在吗"})
	f.receive(t, channeladapter.InboundEvent{Sender: "zhaoliu", Text: "还在吗"})
	require.Len(t, f.outbound.take(), 2, "want uncertain link and one delivered link")
}

// TestWeComBotChangeUnbindsAccounts 验证更换机器人后原有成员绑定失效，外部账号再发消息时重新收到绑定链接。
func TestWeComBotChangeUnbindsAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)
	f.receive(t, channeladapter.InboundEvent{Sender: "sunqi", Text: "你好"})
	token := bindingTokenPattern.FindStringSubmatch(f.outbound.take()[0].body)[1]
	_, err := channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, token)
	require.NoError(t, err)
	f.receive(t, channeladapter.InboundEvent{Sender: "sunqi", Text: "旧机器人上的问题"})
	rows := f.requesterConversations(t, "sunqi")
	require.Len(t, rows, 1)
	recipientBound := func() bool {
		var route deliveryaction.Route
		err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			route, err = deliveryaction.Prepare(ctx, tx, f.identity.Workspace.ID, rows[0].ConversationID)
			return err
		})
		require.NoError(t, err)
		return route.RecipientBound
	}
	require.True(t, recipientBound(), "绑定中的发起成员应可接收回复")
	// 失败的投递只在对方仍是会话发起人时可以人工重试。
	var messageID string
	require.NoError(t, f.db.NewSelect().TableExpr("messages").Column("id").Where("conversation_id = ?", rows[0].ConversationID).Limit(1).Scan(ctx, &messageID))
	require.NoError(t, f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		route, err := deliveryaction.Prepare(ctx, tx, f.identity.Workspace.ID, rows[0].ConversationID)
		if err != nil {
			return err
		}
		if err := deliveryaction.Enqueue(ctx, tx, nil, route, &models.Message{ID: messageID, WorkspaceID: f.identity.Workspace.ID, ConversationID: rows[0].ConversationID}); err != nil {
			return err
		}
		_, err = tx.NewUpdate().TableExpr("channel_message_deliveries").Set("status = 'failed', last_error = 'unknown_result'").Where("message_id = ?", messageID).Exec(ctx)
		return err
	}))
	canRetry := func() bool {
		records, err := deliveryaction.ListForMessages(ctx, f.db, f.identity.Workspace.ID, rows[0].ConversationID, []string{messageID})
		require.NoError(t, err)
		require.Len(t, records, 1)
		return records[0].CanRetry
	}
	require.True(t, canRetry(), "绑定中的发起成员的失败投递应可重试")
	f.botID = "bot-" + uuid.NewV7().String()
	_, err = channelaction.NewSaveWeComBotConnectionAction(f.db).Execute(ctx, f.identity, f.channel.ID, channelaction.WeComBotConnectionInput{BotID: f.botID, Secret: "secret"})
	require.NoError(t, err)
	// 更换机器人后旧会话不再向同一外部编号投递回复。
	require.False(t, recipientBound() || canRetry(), "更换机器人后旧会话不应再投递或重试")
	f.receive(t, channeladapter.InboundEvent{Sender: "sunqi", Text: "换了机器人"})
	sends := f.outbound.take()
	require.Len(t, sends, 1)
	require.Regexp(t, bindingTokenPattern, sends[0].body, "更换机器人后应重新收到绑定链接")
	require.Len(t, f.requesterConversations(t, "sunqi"), 1)
}

// TestEmployeeChannelDecisionReminder 验证渠道会话中等待发起成员确认的操作经渠道发送处理链接，只提醒一次，对方解除绑定后不再提醒。
func TestEmployeeChannelDecisionReminder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)
	f.receive(t, channeladapter.InboundEvent{Sender: "zhouba", Text: "你好"})
	token := bindingTokenPattern.FindStringSubmatch(f.outbound.take()[0].body)[1]
	_, err := channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, token)
	require.NoError(t, err)
	f.receive(t, channeladapter.InboundEvent{Sender: "zhouba", Text: "帮我改一下备注"})
	rows := f.requesterConversations(t, "zhouba")
	require.Len(t, rows, 1)
	var run models.AgentRun
	require.NoError(t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ?", rows[0].ConversationID).Limit(1).Scan(ctx))

	// newCall 为运行写入一次等待处理的工具调用。
	assignee := loadIdentitySubjectID(t, f.db, f.identity.Workspace.ID, f.identity.WorkspaceIdentity.ID)
	newCall := func(intervention domain.ToolIntervention, expiresIn time.Duration) string {
		call := &models.AgentToolCall{
			ID: uuid.NewV7().String(), WorkspaceID: f.identity.Workspace.ID, AgentRunID: run.ID, ModelCallID: uuid.NewV7().String(),
			ProviderCallID: uuid.NewV7().String(), Name: "update_note", Source: "business_system", Arguments: "{}",
			Status: string(domain.AgentToolCallAwaitingDecision), Intervention: new(string(intervention)), AssigneeSubjectID: &assignee,
			ExpiresAt: new(time.Now().Add(expiresIn)),
		}
		_, err := f.db.NewInsert().Model(call).Column("id", "workspace_id", "agent_run_id", "model_call_id", "provider_call_id", "name", "source", "arguments",
			"replayable", "side_effects", "status", "intervention", "assignee_subject_id", "expires_at").Exec(ctx)
		require.NoError(t, err)
		return call.ID
	}
	reminderTasks := servertest.NewTasks()
	reminder := tooldecisionaction.NewReminderAction(f.db, reminderTasks, func() string { return "https://luway.test" })
	remind := func(callID string) {
		require.NoError(t, reminder.Execute(ctx, agentprocess.ToolCallInput{WorkspaceID: f.identity.Workspace.ID, ToolCallID: callID}))
	}
	reminders := func() []string {
		var bodies []string
		require.NoError(t, f.db.NewSelect().TableExpr("messages AS msg").Column("msg.body").
			Join("JOIN channel_message_deliveries AS cmd ON cmd.message_id = msg.id").
			Where("msg.conversation_id = ? AND msg.idempotency_key LIKE 'agent_tool_call_reminder:%'", rows[0].ConversationID).
			OrderExpr("msg.created_at").Scan(ctx, &bodies))
		return bodies
	}

	confirm := newCall(domain.ToolInterventionConfirmation, domain.ToolDecisionTimeout)
	remind(confirm)
	remind(confirm)
	bodies := reminders()
	require.Len(t, bodies, 1, "同一操作只提醒一次")
	require.Contains(t, bodies[0], tooldecisionaction.ToolDecisionLink("https://luway.test", f.identity.Workspace.Slug, confirm))
	require.Contains(t, bodies[0], "IT 助手")

	// 已过截止时间的调用不再提醒。
	remind(newCall(domain.ToolInterventionConfirmation, -time.Minute))
	require.Len(t, reminders(), 1, "已过截止时间的调用不应提醒")

	// 审批由负责人在应用中处理，不经渠道提醒。
	remind(newCall(domain.ToolInterventionApproval, domain.ToolDecisionTimeout))
	require.Len(t, reminders(), 1)

	// 对方解除绑定后不再向其提醒。
	accounts, err := channelbindingaction.NewListAccountsQuery(f.db).Execute(ctx, f.identity, f.channel.ID)
	require.NoError(t, err)
	require.NoError(t, channelbindingaction.NewManageAction(f.db).Unbind(ctx, f.identity, f.channel.ID, accounts[0].ID))
	remind(newCall(domain.ToolInterventionConfirmation, domain.ToolDecisionTimeout))
	require.Len(t, reminders(), 1)
}

// TestEmployeeChannelConfirmationEnqueuesReminder 验证 AI 员工在企业微信会话中提交需要发起成员确认的业务操作时投递确认提醒任务。
func TestEmployeeChannelConfirmationEnqueuesReminder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)
	system := &models.BusinessSystem{
		WorkspaceID: f.identity.Workspace.ID, Name: "工单系统", Transport: domain.BusinessSystemTransportHTTP,
		Connection: domain.BusinessSystemConnection{HTTP: &domain.HTTPConnection{BaseURL: "https://tickets.example.com", Spec: "{}"}},
		Credential: domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialNone},
		Tools: []domain.BusinessTool{{Name: "update_note", Description: "修改备注", DestructiveHint: new(false),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"number"}}}`),
			HTTP:        &domain.HTTPOperation{Method: "PATCH", Path: "/notes", Parameters: []domain.HTTPParameter{{Name: "amount", In: domain.HTTPParameterInBody, Key: "amount"}}}}},
	}
	_, err := f.db.NewInsert().Model(system).Column("workspace_id", "name", "transport", "connection", "credential", "tools").Returning("id").Exec(ctx)
	require.NoError(t, err)
	var agentID string
	require.NoError(t, f.db.NewSelect().TableExpr("agents").Column("id").Where("identity_id = ?", f.agentID).Scan(ctx, &agentID))
	_, err = f.db.NewUpdate().TableExpr("agents").Set("responsible_user_id = ?", f.identity.User.ID).Where("id = ?", agentID).Exec(ctx)
	require.NoError(t, err)
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: f.modelID, SystemInstruction: "回答同事问题"}}
	_, err = agentaction.NewUpdateExecutionAction(f.db).Execute(ctx, f.identity, agentID, agentaction.UpdateExecutionInput{
		ExecutionInput: execution, BusinessSystems: []domain.BusinessSystemGrant{{BusinessSystemID: system.ID, MaxLevel: domain.OperationLevelL2, ConfirmL2: true}},
	})
	require.NoError(t, err)

	f.receive(t, channeladapter.InboundEvent{Sender: "wujiu", Text: "你好"})
	token := bindingTokenPattern.FindStringSubmatch(f.outbound.take()[0].body)[1]
	_, err = channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &models.AccountIdentity{Account: f.identity.Account}, token)
	require.NoError(t, err)
	f.receive(t, channeladapter.InboundEvent{Sender: "wujiu", Text: "帮我改备注"})
	rows := f.requesterConversations(t, "wujiu")
	require.Len(t, rows, 1)

	tasks := servertest.NewTasks()
	runtime, err := agentruntime.New()
	require.NoError(t, err)
	chat := &scriptedDecisionModel{}
	chat.use("［业务系统 · 工单系统］修改备注")
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error) { return chat, nil }
	runner := newTestAgentRun(f.db, tasks, runtime, modelcall.New(f.db, upstreams, nil), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	var run models.AgentRun
	require.NoError(t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", rows[0].ConversationID, domain.AgentRunStatusQueued).Scan(ctx))
	require.NoError(t, runner.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))

	var call models.AgentToolCall
	require.NoError(t, f.db.NewSelect().Model(&call).Where("atc.agent_run_id = ? AND atc.business_system_id IS NOT NULL", run.ID).Scan(ctx))
	require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
	require.Equal(t, string(domain.ToolInterventionConfirmation), *call.Intervention)
	reminders := 0
	for _, task := range tasks.Queued(tooldecisionaction.ToolDecisionReminderActionName, "") {
		if task.Options.IdempotencyKey == "agent-tool-call-reminder:"+call.ID {
			reminders++
		}
	}
	require.Equal(t, 1, reminders, "渠道会话的确认应投递提醒任务")
	// 长连接渠道的立即推进按渠道长连接路由，由持有连接的服务端实例认领。
	var identityIDs []string
	require.NoError(t, f.db.NewSelect().TableExpr("channel_message_deliveries").ColumnExpr("channel_identity_id::text").
		Where("conversation_id = ?", rows[0].ConversationID).Scan(ctx, &identityIDs))
	routes := make([]string, 0)
	for _, task := range tasks.Queued(deliveryaction.AdvanceActionName, "") {
		if task.Options.IdempotencyKey == "" && slices.Contains(identityIDs, servertest.TaskPayload[deliveryaction.AdvanceInput](t, task).ChannelIdentityID) {
			routes = append(routes, task.Options.Route)
		}
	}
	require.NotEmpty(t, routes, "确认提示应投递外发任务")
	for _, route := range routes {
		require.Equal(t, channeladapter.ConnectionRoute(f.channel.ID), route)
	}
}
