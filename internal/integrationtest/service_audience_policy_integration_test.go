//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"

	agentevaluationaction "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	channelbindingaction "github.com/runforyou-ai/luway/internal/actions/channelbinding"
	"github.com/runforyou-ai/luway/internal/actions/reportpage"
	"github.com/runforyou-ai/luway/internal/actions/servicepolicy"
	"github.com/runforyou-ai/luway/internal/actions/servicetimeout"
	teamperformanceaction "github.com/runforyou-ai/luway/internal/actions/teamperformance"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestServiceAudiencePolicy 验证同一工作区两类服务的配置、分类与渠道管理权限独立，普通成员仍能读取渠道选项。
func TestServiceAudiencePolicy(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	workspaceID := f.owner.Workspace.ID
	customer, employee := domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee
	customerTimeouts := domain.ServiceTimeouts{ResponseReminderMinutes: 2, ResponseReclaimMinutes: 4, QueueReminderMinutes: 3, AIFollowUpMinutes: 5, AICloseMinutes: 8}
	_, err := servicepolicy.NewUpdateServiceTimeoutsAction(f.db).Execute(ctx, f.owner, customer, customerTimeouts)
	require.NoError(t, err)
	employeeTimeouts, err := servicepolicy.LoadServiceTimeouts(ctx, f.db, workspaceID, employee)
	require.NoError(t, err)
	require.Equal(t, 10, employeeTimeouts.AIFollowUpMinutes)
	require.Equal(t, 30, employeeTimeouts.AICloseMinutes)

	providerID := seedSummaryModels(t, f.db, f.owner)
	modelID := aiModelID(t, f.db, providerID, "decision-model")
	summaryModelID := aiModelID(t, f.db, providerID, "chat-model")
	_, err = servicepolicy.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, customer, domain.ServiceSummarySettings{DecisionModelID: &modelID, SummaryModelID: &summaryModelID, Locale: domain.LocaleEnglishUnitedStates})
	require.NoError(t, err)
	employeeSummary, err := servicepolicy.LoadServiceSummarySettings(ctx, f.db, workspaceID, employee)
	require.NoError(t, err)
	require.Nil(t, employeeSummary.DecisionModelID)
	require.Nil(t, employeeSummary.SummaryModelID)
	evaluationModel, err := agentevaluationaction.NewSettingsQuery(f.db).Execute(ctx, f.owner)
	require.NoError(t, err)
	require.Nil(t, evaluationModel)
	_, err = agentevaluationaction.NewUpdateSettingsAction(f.db).Execute(ctx, f.owner, &modelID)
	require.NoError(t, err)
	_, err = servicepolicy.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, customer, domain.ServiceSummarySettings{Locale: domain.LocaleChineseSimplified})
	require.NoError(t, err)
	evaluationModel, err = agentevaluationaction.NewSettingsQuery(f.db).Execute(ctx, f.owner)
	require.NoError(t, err)
	require.Equal(t, &modelID, evaluationModel)
	require.ErrorIs(t, aiprovideraction.NewDeleteAIProviderAction(f.db).Execute(ctx, f.owner, providerID), aiprovideraction.ErrInUse)
	_, err = agentevaluationaction.NewUpdateSettingsAction(f.db).Execute(ctx, f.owner, nil)
	require.NoError(t, err)
	_, err = servicepolicy.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, employee, domain.ServiceSummarySettings{SummaryModelID: &summaryModelID, Locale: domain.LocaleChineseSimplified})
	require.NoError(t, err)
	require.ErrorIs(t, aiprovideraction.NewDeleteAIProviderAction(f.db).Execute(ctx, f.owner, providerID), aiprovideraction.ErrInUse)

	categories := servicepolicy.NewCreateAction(f.db)
	customerCategory, err := categories.Execute(ctx, f.owner, customer, servicepolicy.Input{Name: "账号问题"})
	require.NoError(t, err)
	employeeCategory, err := categories.Execute(ctx, f.owner, employee, servicepolicy.Input{Name: "账号问题"})
	require.NoError(t, err)
	require.NotEqual(t, customerCategory.ID, employeeCategory.ID)
	found, err := servicepolicy.FindActive(ctx, f.db, workspaceID, employee, customerCategory.ID)
	require.NoError(t, err)
	require.Nil(t, found)
	active, err := servicepolicy.Active(ctx, f.db, workspaceID, employee)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, employeeCategory.ID, active[0].ID)

	create := channelaction.NewCreateMessageChannelAction(f.db)
	for _, kind := range []domain.ChannelType{domain.ChannelTypeWebsite, domain.ChannelTypeWeComBot} {
		_, err := create.Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{Type: kind, Name: string(kind), DefaultLocale: domain.CustomerLocaleChineseSimplified, NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}})
		require.NoError(t, err)
	}
	employeeManager := *f.owner
	employeeManager.Role = servermodels.MemberRole{Kind: domain.RoleKindCustom, Permissions: []domain.PermissionCode{domain.PermissionEmployeeServiceManage}}
	_, err = servicepolicy.NewGetServiceTimeoutsQuery(f.db).Execute(ctx, &employeeManager, customer)
	require.ErrorIs(t, err, servicepolicy.ErrForbidden)
	_, err = servicepolicy.NewGetServiceTimeoutsQuery(f.db).Execute(ctx, &employeeManager, employee)
	require.NoError(t, err)
	listed, err := channelaction.NewListMessageChannelsQuery(f.db).Execute(ctx, &employeeManager)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, string(domain.ChannelTypeWeComBot), listed[0].Type)
	_, err = channelaction.NewGetMessageChannelQuery(f.db).Execute(ctx, &employeeManager, listed[0].ID)
	require.NoError(t, err)
	ordinary := *f.owner
	ordinary.Role = servermodels.MemberRole{Kind: domain.RoleKindMember}
	listed, err = channelaction.NewListMessageChannelsQuery(f.db).ExecuteForMembers(ctx, &ordinary)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	managed, err := channelaction.NewListMessageChannelsQuery(f.db).Execute(ctx, &ordinary)
	require.ErrorIs(t, err, servicepolicy.ErrForbidden)
	require.Empty(t, managed)
	for _, item := range listed {
		_, err := channelaction.NewGetMessageChannelQuery(f.db).Execute(ctx, &ordinary, item.ID)
		require.ErrorIs(t, err, servicepolicy.ErrForbidden)
	}
}

// TestServiceAudienceTimeout 验证员工单聊使用独立的自动跟进和结束时长，已排队的超时任务按最新配置重新判断。
func TestServiceAudienceTimeout(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	h := handoffFixture{db: f.db, identity: f.owner, tasks: f.tasks, modelID: f.execution.Managed.ModelID}
	worker := servicetimeout.NewWorker(f.db, testEnqueuer, agentrunaction.NewScheduler(f.tasks))
	r := resolutionFixture{handoffFixture: h, timeouts: worker}
	customer := f.newAgent(t, "客户服务", domain.ServiceAudienceCustomer)
	channelID := h.newChannel(t, customer.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	received := h.receive(t, &input, "如何修改账号")
	customerRun := h.executeQueuedRun(t, received.Conversation.ID, resolutionRuntime("打开设置修改", agentcontract.TerminalDecision{}, nil, nil))
	directID := f.startChat(t, f.agent.IdentityID, "如何申请权限")
	employeeRun := h.executeQueuedRun(t, directID, resolutionRuntime("请填写权限申请", agentcontract.TerminalDecision{}, nil, nil))
	settings := domain.ServiceTimeouts{ResponseReminderMinutes: 5, ResponseReclaimMinutes: 15, QueueReminderMinutes: 5, AIFollowUpMinutes: 2, AICloseMinutes: 4}
	_, err := servicepolicy.NewUpdateServiceTimeoutsAction(f.db).Execute(ctx, f.owner, domain.ServiceAudienceCustomer, settings)
	require.NoError(t, err)
	for _, id := range []string{customerRun.ScopeID, employeeRun.ScopeID} {
		r.age(t, id, 3)
	}
	require.NoError(t, worker.Scan(ctx, struct{}{}))
	require.True(t, timeoutTaskQueued(f.owner.Workspace.ID, customerRun.ScopeID))
	require.False(t, timeoutTaskQueued(f.owner.Workspace.ID, employeeRun.ScopeID))
	r.process(t, customerRun.ScopeID)
	h.executeQueuedRun(t, received.Conversation.ID, resolutionRuntime("账号修改还有需要帮助的吗？", agentcontract.TerminalDecision{}, nil, nil))
	r.process(t, employeeRun.ScopeID)
	require.NotNil(t, loadSession(t, f.db, customerRun.ScopeID).ResolutionRequestedAt)
	require.Nil(t, loadSession(t, f.db, employeeRun.ScopeID).ResolutionRequestedAt)

	// 员工时限到达后进入跟进，后续配置延长会阻止旧超时任务结束服务。
	r.age(t, employeeRun.ScopeID, 11)
	r.process(t, employeeRun.ScopeID)
	followUp := h.executeQueuedRun(t, directID, resolutionRuntime("权限申请还有需要协助的地方吗？", agentcontract.TerminalDecision{}, nil, nil))
	require.Equal(t, string(domain.AgentRunStatusSucceeded), followUp.Status)
	require.NotNil(t, loadSession(t, f.db, employeeRun.ScopeID).ResolutionRequestedAt)
	r.age(t, employeeRun.ScopeID, 31)
	require.NoError(t, worker.Scan(ctx, struct{}{}))
	require.True(t, timeoutTaskQueued(f.owner.Workspace.ID, employeeRun.ScopeID))
	settings.AIFollowUpMinutes, settings.AICloseMinutes = 20, 60
	_, err = servicepolicy.NewUpdateServiceTimeoutsAction(f.db).Execute(ctx, f.owner, domain.ServiceAudienceEmployee, settings)
	require.NoError(t, err)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), r.process(t, employeeRun.ScopeID).Status)
	r.age(t, employeeRun.ScopeID, 61)
	assertAgentClosed(t, f.db, r.process(t, employeeRun.ScopeID), f.agent.IdentityID, domain.ServiceSessionCloseCustomerUnresponsive)
}

// TestServiceAudienceReports 验证服务报表按对象与来源筛选，并保持全部服务等于两类服务之和。
func TestServiceAudienceReports(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	h := handoffFixture{db: f.db, identity: f.owner, tasks: f.tasks, modelID: f.execution.Managed.ModelID}
	customer := f.newAgent(t, "客户服务", domain.ServiceAudienceCustomer)
	channelID := h.newChannel(t, customer.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	received := h.receive(t, &input, "问题解决了")
	directID := f.startChat(t, f.agent.IdentityID, "权限已开通")
	for _, conversationID := range []string{received.Conversation.ID, directID} {
		h.executeQueuedRun(t, conversationID, resolutionRuntime("处理完成", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	}
	for _, scope := range []struct {
		audience domain.ServiceAudience
		source   domain.ServiceSource
		count    int
	}{
		{"", "", 2}, {domain.ServiceAudienceCustomer, "", 1}, {domain.ServiceAudienceEmployee, "", 1}, {"", domain.ServiceSourceDirect, 1}, {"", domain.ServiceSourceChannel, 1},
	} {
		ai, err := aiperformanceaction.NewOverviewQuery(f.db).Execute(ctx, f.owner, aiperformanceaction.Input{Days: 7, Audience: scope.audience, ServiceSource: scope.source})
		require.NoError(t, err)
		require.Equal(t, scope.count, ai.Summary.Closed)
		team, err := teamperformanceaction.NewOverviewQuery(f.db).Execute(ctx, f.owner, teamperformanceaction.Input{Days: 7, Audience: scope.audience, ServiceSource: scope.source})
		require.NoError(t, err)
		require.Equal(t, scope.count, team.Closed)
	}
	for _, scope := range []aiperformanceaction.Input{
		{Days: 7, Audience: domain.ServiceAudienceCustomer, ServiceSource: domain.ServiceSourceDirect},
		{Days: 7, Audience: domain.ServiceAudienceEmployee, ChannelID: channelID},
		{Days: 7, ServiceSource: domain.ServiceSourceDirect, ChannelID: channelID},
	} {
		_, err := aiperformanceaction.NewOverviewQuery(f.db).Execute(ctx, f.owner, scope)
		require.ErrorIs(t, err, reportpage.ErrServiceScopeInvalid)
	}
}

// TestServiceAudienceSummary 验证员工小结实际使用员工模型与分类，并拒绝模型输出和人工编辑中的客户分类。
func TestServiceAudienceSummary(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	providerID := seedSummaryModels(t, f.db, f.owner)
	decisionID, summaryID := aiModelID(t, f.db, providerID, "decision-model"), aiModelID(t, f.db, providerID, "chat-model")
	_, err := servicepolicy.NewUpdateServiceSummarySettingsAction(f.db).Execute(ctx, f.owner, domain.ServiceAudienceEmployee, domain.ServiceSummarySettings{DecisionModelID: &decisionID, SummaryModelID: &summaryID, Locale: domain.LocaleEnglishUnitedStates})
	require.NoError(t, err)
	customerCategory, err := servicepolicy.NewCreateAction(f.db).Execute(ctx, f.owner, domain.ServiceAudienceCustomer, servicepolicy.Input{Name: "客户订单"})
	require.NoError(t, err)
	employeeCategory, err := servicepolicy.NewCreateAction(f.db).Execute(ctx, f.owner, domain.ServiceAudienceEmployee, servicepolicy.Input{Name: "员工权限"})
	require.NoError(t, err)
	conversationID := f.startChat(t, f.agent.IdentityID, "权限申请已经通过，谢谢")
	h := handoffFixture{db: f.db, identity: f.owner, tasks: f.tasks}
	run := h.executeQueuedRun(t, conversationID, resolutionRuntime("已处理完成", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	input := loadSummarizeInput(t, run.ScopeID, f.tasks, testEnqueuer)
	decider := &summaryDecider{answers: map[string]decision.Answer{"category": {Kind: decision.KindChoice, Choice: customerCategory.ID, Probabilities: map[string]float64{customerCategory.ID: 1}}}}
	caller := &summaryCaller{text: `{"summary":"Employee access was granted."}`}
	worker := servicesummary.NewWorker(f.db, f.tasks, modelcall.New(f.db, modelcall.Upstreams{Decider: decider, Chat: caller.chat}, nil))
	require.NoError(t, worker.Summarize(ctx, input))
	require.Equal(t, 1, caller.calls)
	require.Contains(t, caller.input, "发起人")
	require.Contains(t, caller.input, "权限申请已经通过")
	options := decider.questions["category"].Options
	require.Contains(t, options, decision.Option{Key: employeeCategory.ID, Description: employeeCategory.Name})
	for _, option := range options {
		require.NotEqual(t, customerCategory.ID, option.Key)
	}
	require.Nil(t, loadSession(t, f.db, run.ScopeID).CategoryID)
	var modelIDs []string
	require.NoError(t, f.db.NewSelect().Table("ai_model_calls").Column("model_id").Where("source_id = ?", run.ScopeID).Scan(ctx, &modelIDs))
	require.Contains(t, modelIDs, decisionID)
	require.Contains(t, modelIDs, summaryID)
	summaries, err := servicesessionaction.NewListServiceSummariesQuery(f.db).Execute(ctx, f.owner, conversationID)
	require.NoError(t, err)
	require.Equal(t, domain.ServiceAudienceEmployee, summaries.Sessions[0].Audience)
	edit := servicesessionaction.NewUpdateServiceSessionSummaryAction(f.db)
	_, err = edit.Execute(ctx, f.owner, servicesessionaction.UpdateServiceSessionSummaryInput{ServiceSessionID: run.ScopeID, Summary: "权限已开通", CategoryID: &customerCategory.ID})
	var fields *common.FieldError
	require.ErrorAs(t, err, &fields)
	require.Equal(t, servicesessionaction.ValidationCategoryIDInvalid, fields.Fields["categoryId"])
	_, err = edit.Execute(ctx, f.owner, servicesessionaction.UpdateServiceSessionSummaryInput{ServiceSessionID: run.ScopeID, Summary: "权限已开通", CategoryID: &employeeCategory.ID})
	require.NoError(t, err)
	require.Empty(t, f.tasks.Queued(servicesummary.ExtractContactProfileActionName, f.owner.Workspace.ID))
}

// TestServiceAudienceBusinessHours 验证员工首响按员工工作时间计算，客户休息日保持无首响时长样本。
func TestServiceAudienceBusinessHours(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	_, err := servicepolicy.NewUpdateBusinessHoursAction(f.db).Execute(ctx, f.owner, domain.ServiceAudienceCustomer, domain.BusinessHours{Enabled: true, TimeZone: "UTC"})
	require.NoError(t, err)
	h := handoffFixture{db: f.db, identity: f.owner, tasks: f.tasks, modelID: f.execution.Managed.ModelID}
	customer := f.newAgent(t, "客户服务", domain.ServiceAudienceCustomer)
	channelID := h.newChannel(t, customer.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	received := h.receive(t, &input, "订单问题")
	directID := f.startChat(t, f.agent.IdentityID, "权限问题")
	for _, item := range []struct {
		conversationID string
		sampled        bool
	}{{received.Conversation.ID, false}, {directID, true}} {
		service := f.service(t, item.conversationID)
		require.NoError(t, serverstorage.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			locked, err := chatstate.LockServiceSessionByID(ctx, tx, f.owner.Workspace.ID, *service.CurrentServiceSessionID)
			if err != nil {
				return err
			}
			now, err := serverstorage.Now(ctx, tx)
			if err != nil {
				return err
			}
			requestedAt := now.Add(-time.Hour)
			if err := servicestate.Begin(locked.Session).Assign(f.member.WorkspaceIdentity.ID, requestedAt).Save(ctx, tx, f.tasks); err != nil {
				return err
			}
			if err := servicestate.RecordStaffReply(ctx, tx, locked.Session, now); err != nil {
				return err
			}
			if item.sampled {
				require.Equal(t, new(3600), locked.Session.HumanFirstResponseSec)
			} else {
				require.Nil(t, locked.Session.HumanFirstResponseSec)
			}
			return nil
		}))
	}
}

// TestServiceAudienceChannelTranslation 验证客服翻译配置启用时，员工渠道的翻译入口仍拒绝调用。
func TestServiceAudienceChannelTranslation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newEmployeeChannelFixture(t)
	f.receive(t, channeladapter.InboundEvent{Sender: "employee-es", Text: "Necesito acceso"})
	sends := f.outbound.take()
	require.Len(t, sends, 1)
	token := bindingTokenPattern.FindStringSubmatch(sends[0].body)
	require.NotNil(t, token)
	_, err := channelbindingaction.NewConfirmAction(f.db).Execute(ctx, &servermodels.AccountIdentity{Account: f.identity.Account}, token[1])
	require.NoError(t, err)
	f.receive(t, channeladapter.InboundEvent{Sender: "employee-es", Text: "Necesito acceso"})
	rows := f.requesterConversations(t, "employee-es")
	require.Len(t, rows, 1)
	conversationID := rows[0].ConversationID
	providerID := seedSummaryModels(t, f.db, f.identity)
	modelID := aiModelID(t, f.db, providerID, "chat-model")
	_, err = translationaction.UpdateTranslationSettings(ctx, f.db, f.identity, &modelID)
	require.NoError(t, err)
	caller := &translationCaller{}
	translator := translationaction.NewTranslator(f.db, chatInvoker(f.db, caller.chat))
	_, err = translator.ConversationState(ctx, f.identity, conversationID)
	require.ErrorIs(t, err, translationaction.ErrConversationNotFound)
	_, err = translator.TranslateReply(ctx, f.identity, conversationID, "你好")
	require.ErrorIs(t, err, translationaction.ErrConversationNotFound)
	_, err = translator.PreviewReply(ctx, f.identity, conversationID, "你好")
	require.ErrorIs(t, err, translationaction.ErrConversationNotFound)
	var messageID string
	require.NoError(t, f.db.NewSelect().Table("messages").Column("id").Where("conversation_id = ? AND body = ?", conversationID, "Necesito acceso").Limit(1).Scan(ctx, &messageID))
	_, err = f.db.NewInsert().Model(&servermodels.MessageTranslation{MessageID: messageID, WorkspaceID: f.identity.Workspace.ID, Language: translationaction.ViewerLanguage(f.identity), Body: "需要权限"}).Exec(ctx)
	require.NoError(t, err)
	_, err = translator.TranslateMessages(ctx, f.identity, conversationID, []string{messageID})
	require.ErrorIs(t, err, translationaction.ErrConversationNotFound)
	_, err = translator.SetReplyLanguage(ctx, f.identity, conversationID, "es")
	require.ErrorIs(t, err, translationaction.ErrConversationNotFound)
	require.ErrorIs(t, translator.ValidateReplyLanguage(ctx, f.identity, conversationID, "es"), translationaction.ErrConversationNotFound)
	var replyLanguage *string
	require.NoError(t, f.db.NewSelect().Table("channel_conversations").Column("reply_language").Where("conversation_id = ?", conversationID).Scan(ctx, &replyLanguage))
	require.Nil(t, replyLanguage)
	require.Zero(t, caller.calls)

}

// TestServiceAudienceChannelStatusPermission 验证专用 Telegram 启停处理器同样执行服务对象权限检查。
func TestServiceAudienceChannelStatusPermission(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{Type: domain.ChannelTypeTelegram, Name: "权限验证", DefaultLocale: domain.CustomerLocaleChineseSimplified, NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}})
	require.NoError(t, err)
	status := channelaction.NewUpdateMessageChannelStatusAction(f.db, f.tasks, map[domain.ChannelType]channelaction.StatusUpdater{
		domain.ChannelTypeTelegram: telegramaction.NewUpdateChannelStatusAction(f.db, nil, nil, f.tasks),
	})
	customerManager := *f.owner
	customerManager.Role = servermodels.MemberRole{Kind: domain.RoleKindCustom, Permissions: []domain.PermissionCode{domain.PermissionCustomerServiceManage}}
	for _, role := range []servermodels.MemberRole{
		{Kind: domain.RoleKindMember},
		{Kind: domain.RoleKindCustom, Permissions: []domain.PermissionCode{domain.PermissionEmployeeServiceManage}},
	} {
		actor := *f.owner
		actor.Role = role
		for _, enabled := range []bool{false, true} {
			_, err := status.Execute(ctx, &customerManager, channel.ID, !enabled)
			require.NoError(t, err)
			_, err = status.Execute(ctx, &actor, channel.ID, enabled)
			require.ErrorIs(t, err, servicepolicy.ErrForbidden)
			var actual bool
			require.NoError(t, f.db.NewSelect().Table("channels").Column("enabled").Where("id = ?", channel.ID).Scan(ctx, &actual))
			require.Equal(t, !enabled, actual)
		}
	}
}

// TestEmployeeDirectTranslationRejected 验证员工单聊拒绝翻译查询和预译文发送，原文回复正常。
func TestEmployeeDirectTranslationRejected(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	conversationID := f.startChat(t, f.agent.IdentityID, "需要权限")
	caller := &translationCaller{}
	translator := translationaction.NewTranslator(f.db, chatInvoker(f.db, caller.chat))
	_, err := translator.ConversationState(ctx, f.owner, conversationID)
	require.ErrorIs(t, err, translationaction.ErrConversationNotFound)
	_, err = translator.TranslateReply(ctx, f.owner, conversationID, "已处理")
	require.ErrorIs(t, err, translationaction.ErrConversationNotFound)
	_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, f.tasks).Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "已处理", Visibility: domain.MessageVisibilityShared,
		Translation: &servicesessionaction.OutgoingTranslation{Language: "en", SourceLanguage: "zh-CN", Body: "Done"},
	})
	var fieldError *common.FieldError
	require.ErrorAs(t, err, &fieldError)
	require.Equal(t, common.FieldCode(servicesessionaction.ValidationTranslationInvalid), fieldError.Fields["translation"])
	_, err = f.reply(conversationID, "已处理", domain.MessageVisibilityInternal)
	require.NoError(t, err)
	require.Zero(t, caller.calls)
}
