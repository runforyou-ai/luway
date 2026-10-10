//go:build server

// 本文件锁定成员发消息流水线的验收行为：成员单聊、群聊、AI 聊天、客服服务会话与 Copilot 线程上
// 文本与附件发送的权限矩阵、按发送编号的幂等与并发收敛，以及消息序号、会话最新消息与阅读水位的一致更新。
package integrationtest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// pipelineFixture 是发送流水线验收测试共用的工作区、成员、AI 员工与发送入口。
type pipelineFixture struct {
	t           *testing.T
	ctx         context.Context
	db          *bun.DB
	owner       *servermodels.Identity // 工作区管理员，开启接待。
	colleague   *servermodels.Identity // 同工作区成员，开启接待。
	handlerless *servermodels.Identity // 同工作区成员，未开启接待。
	outsider    *servermodels.Identity // 其他工作区的管理员。
	modelID     string
	tasks       *servertest.Tasks
	scheduler   *agentrunaction.Scheduler
	agent       *agentaction.Agent // 内部 AI 员工。
	copilot     *agentaction.Agent // 服务客户的 AI 员工，用于 Copilot 线程。
	channelID   string             // 公共队列接待的网站渠道。
}

// pipelineKind 是一种会话类型的文本与附件发送入口。
type pipelineKind struct {
	name       string
	text       func(sender *servermodels.Identity, conversationID, clientID, body, replyTo string) (conversationaction.ConversationMessage, error)
	attachment func(sender *servermodels.Identity, conversationID, clientID, fileID, body string) (conversationaction.ConversationMessage, error)
}

// newPipelineFixture 建立带对话模型的工作区、三名成员、外部工作区管理员、两个 AI 员工和一个网站渠道。
func newPipelineFixture(t *testing.T) *pipelineFixture {
	t.Helper()
	db, owner, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	f := &pipelineFixture{t: t, ctx: ctx, db: db, owner: owner, modelID: modelID}
	f.colleague = newChatLockUser(t, db, owner)
	email := servertest.UniqueEmail("handlerless")
	_, err := newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{DisplayName: "未接待成员", Email: email, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	f.handlerless = servertest.LoginMember(t, db, owner.Workspace.ID, email, "password123").Identity
	f.outsider = servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "外部工作区", DisplayName: "外部管理员", Email: servertest.UniqueEmail("outsider"), Password: "password123"}).Identity
	f.tasks = servertest.NewTasks()
	f.scheduler = agentrunaction.NewScheduler(f.tasks)
	managed := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "按会话回答"}}
	f.agent, err = agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{DisplayName: "流水线助手", Execution: managed})
	require.NoError(t, err)
	f.copilot, err = agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{
		DisplayName: "流水线 Copilot", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, Execution: managed,
	})
	require.NoError(t, err)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "流水线渠道", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	f.channelID = channel.ID
	return f
}

// kinds 返回五种会话类型的发送入口，内部会话附件共用同一个附件发送操作。
func (f *pipelineFixture) kinds() map[string]pipelineKind {
	ctx, db := f.ctx, f.db
	internalAttachment := func(sender *servermodels.Identity, conversationID, clientID, fileID, body string) (conversationaction.ConversationMessage, error) {
		result, err := directchataction.NewSendAttachmentMessageAction(db, testEnqueuer, f.scheduler).Execute(ctx, sender, directchataction.AttachmentMessageInput{
			ConversationID: conversationID, ClientMessageID: clientID, FileID: fileID, Body: body,
		})
		return result.Message, err
	}
	return map[string]pipelineKind{
		"direct": {name: "direct", attachment: internalAttachment,
			text: func(sender *servermodels.Identity, conversationID, clientID, body, replyTo string) (conversationaction.ConversationMessage, error) {
				return directchataction.NewSendDirectTextMessageAction(db, testEnqueuer).Execute(ctx, sender, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: clientID, Body: body, ReplyToMessageID: replyTo})
			}},
		"group": {name: "group", attachment: internalAttachment,
			text: func(sender *servermodels.Identity, conversationID, clientID, body, replyTo string) (conversationaction.ConversationMessage, error) {
				return newGroupSendAction(db).Execute(ctx, sender, groupchataction.GroupTextMessageInput{ConversationID: conversationID, ClientMessageID: clientID, Body: body, ReplyToMessageID: replyTo})
			}},
		"agent": {name: "agent", attachment: internalAttachment,
			text: func(sender *servermodels.Identity, conversationID, clientID, body, replyTo string) (conversationaction.ConversationMessage, error) {
				return directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, f.scheduler).Execute(ctx, sender, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: clientID, Body: body, ReplyToMessageID: replyTo})
			}},
		"service": {name: "service",
			text: func(sender *servermodels.Identity, conversationID, clientID, body, replyTo string) (conversationaction.ConversationMessage, error) {
				return servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, sender, servicesessionaction.ServiceTextMessageInput{ConversationID: conversationID, ClientMessageID: clientID, Body: body, ReplyToMessageID: replyTo})
			},
			attachment: func(sender *servermodels.Identity, conversationID, clientID, fileID, body string) (conversationaction.ConversationMessage, error) {
				return servicesessionaction.NewSendServiceAttachmentMessageAction(db, testEnqueuer).Execute(ctx, sender, servicesessionaction.ServiceAttachmentMessageInput{ConversationID: conversationID, ClientMessageID: clientID, FileID: fileID, Body: body})
			}},
		"copilot": {name: "copilot", attachment: internalAttachment,
			text: func(sender *servermodels.Identity, conversationID, clientID, body, replyTo string) (conversationaction.ConversationMessage, error) {
				return directchataction.NewSendServiceCopilotTextMessageAction(db, testEnqueuer, f.scheduler).Execute(ctx, sender, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: clientID, Body: body, ReplyToMessageID: replyTo})
			}},
	}
}

// directConversation 由 owner 向 peer 首发一条消息并返回长期单聊编号。
func (f *pipelineFixture) directConversation(owner, peer *servermodels.Identity) string {
	f.t.Helper()
	result, err := directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(f.ctx, owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: peer.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊首条",
	})
	require.NoError(f.t, err)
	return result.Conversation.ID
}

// groupConversation 由 owner 创建包含指定成员的群聊。
func (f *pipelineFixture) groupConversation(members ...*servermodels.Identity) string {
	f.t.Helper()
	ids := arr.Map(members, func(member *servermodels.Identity) string { return member.WorkspaceIdentity.ID })
	group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(f.ctx, f.owner, groupchataction.GroupConversationInput{Title: "流水线群", MemberIdentityIDs: ids})
	require.NoError(f.t, err)
	return group.ID
}

// agentConversation 由 owner 与指定 AI 员工身份首发 AI 聊天并返回会话编号。
func (f *pipelineFixture) agentConversation(agentIdentityID string) string {
	f.t.Helper()
	conversationID := uuid.NewV7().String()
	_, err := directchataction.NewSendFirstAgentTextMessageAction(f.db, testEnqueuer, f.scheduler).Execute(f.ctx, f.owner, directchataction.FirstAgentTextMessageInput{
		ConversationID: conversationID, AgentIdentityID: agentIdentityID, ClientMessageID: uuid.NewV7().String(), Body: "AI 聊天首条",
	})
	require.NoError(f.t, err)
	return conversationID
}

// serviceConversation 以新的网站访客发来首条消息，返回进入公共队列的客服会话编号。
func (f *pipelineFixture) serviceConversation() string {
	f.t.Helper()
	result, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, f.scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(f.ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "访客首条",
	})
	require.NoError(f.t, err)
	return result.Conversation.ID
}

// copilotThread 由 owner 在新的客服会话上首发 Copilot 线程并返回线程编号。
func (f *pipelineFixture) copilotThread() string {
	f.t.Helper()
	threadID := uuid.NewV7().String()
	_, err := directchataction.NewSendFirstServiceCopilotMessageAction(f.db, testEnqueuer, f.scheduler).Execute(f.ctx, f.owner, directchataction.FirstServiceCopilotMessageInput{
		ThreadID: threadID, ServedConversationID: f.serviceConversation(), AgentIdentityID: f.copilot.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "Copilot 首问",
	})
	require.NoError(f.t, err)
	return threadID
}

// attachmentFile 为发送者上传一个尚未发送的图片附件。
func (f *pipelineFixture) attachmentFile(sender *servermodels.Identity) string {
	f.t.Helper()
	return uploadedAttachment(f.t, f.db, sender, "pipeline.png", "image/png")
}

// pipelineExpect 断言一次发送的结果。
type pipelineExpect func(t *testing.T, err error, label string)

// pipelineAccepted 断言发送被接受。
func pipelineAccepted(t *testing.T, err error, label string) {
	t.Helper()
	require.NoError(t, err, label)
}

// pipelineRejected 断言发送以指定错误被拒绝。
func pipelineRejected(target error) pipelineExpect {
	return func(t *testing.T, err error, label string) {
		t.Helper()
		require.ErrorIs(t, err, target, label)
	}
}

// pipelineConflict 断言发送以指定原因的写入冲突被拒绝。
func pipelineConflict(reason string) pipelineExpect {
	return func(t *testing.T, err error, label string) {
		t.Helper()
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, label)
		require.Equal(t, reason, conflict.Reason, label)
	}
}

// pipelineCase 是发送权限矩阵中的一格：发送者、目标会话与文本、附件各自的期望结果。
type pipelineCase struct {
	name           string
	sender         func() *servermodels.Identity
	conversationID func() string
	text           pipelineExpect
	attachment     pipelineExpect
}

// runPipelineCases 依次执行矩阵格，每格分别以文本与附件发送并断言结果。
func runPipelineCases(t *testing.T, f *pipelineFixture, kind pipelineKind, cases []pipelineCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sender, conversationID := c.sender(), c.conversationID()
			_, err := kind.text(sender, conversationID, uuid.NewV7().String(), "矩阵文本", "")
			c.text(t, err, kind.name+" text "+c.name)
			_, err = kind.attachment(sender, conversationID, uuid.NewV7().String(), f.attachmentFile(sender), "矩阵附件")
			c.attachment(t, err, kind.name+" attachment "+c.name)
		})
	}
}

// pipelineIdentity 返回固定身份的取值函数。
func pipelineIdentity(identity *servermodels.Identity) func() *servermodels.Identity {
	return func() *servermodels.Identity { return identity }
}

// pipelineValue 返回固定会话编号的取值函数。
func pipelineValue(id string) func() string { return func() string { return id } }

// TestMemberSendPipelinePermissions 验证五种会话类型在各发送资格条件下文本与附件发送的接受与拒绝结论一致。
func TestMemberSendPipelinePermissions(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture(t)
	kinds := f.kinds()
	notFound := pipelineRejected(conversationaction.ErrConversationNotFound)

	t.Run("成员单聊", func(t *testing.T) {
		conversationID := f.directConversation(f.owner, f.colleague)
		peerDisabled := f.directConversation(f.owner, f.handlerless)
		// 个人归档后发送：发送被接受并清除归档。
		archived := f.directConversation(f.owner, f.colleague)
		runPipelineCases(t, f, kinds["direct"], []pipelineCase{
			{"参与者发送", pipelineIdentity(f.owner), pipelineValue(conversationID), pipelineAccepted, pipelineAccepted},
			{"对端发送", pipelineIdentity(f.colleague), pipelineValue(conversationID), pipelineAccepted, pipelineAccepted},
			{"同工作区非参与者", pipelineIdentity(f.handlerless), pipelineValue(conversationID), notFound, notFound},
			{"其他工作区成员", pipelineIdentity(f.outsider), pipelineValue(conversationID), notFound, notFound},
			{"个人归档后发送", pipelineIdentity(f.owner), func() string {
				require.NoError(t, conversationaction.NewUpdateConversationArchiveAction(f.db).Execute(f.ctx, f.owner, archived, true))
				return archived
			}, pipelineAccepted, pipelineAccepted},
			{"对端账号停用", pipelineIdentity(f.owner), func() string {
				_, err := testUserStatusAction(f.db).Execute(f.ctx, f.owner, f.handlerless.User.ID, domain.IdentityStatusInactive)
				require.NoError(t, err)
				t.Cleanup(func() {
					_, _ = testUserStatusAction(f.db).Execute(context.Background(), f.owner, f.handlerless.User.ID, domain.IdentityStatusActive)
				})
				return peerDisabled
			}, notFound, notFound},
		})
		pipelineRequireArchiveCleared(t, f, archived)
	})

	t.Run("群聊", func(t *testing.T) {
		groupID := f.groupConversation(f.colleague)
		removed := f.groupConversation(f.colleague)
		left := f.groupConversation(f.colleague)
		dissolved := f.groupConversation(f.colleague)
		archived := f.groupConversation(f.colleague)
		coordinator := newGroupAgentCoordinator(f.db)
		runPipelineCases(t, f, kinds["group"], []pipelineCase{
			{"群主发送", pipelineIdentity(f.owner), pipelineValue(groupID), pipelineAccepted, pipelineAccepted},
			{"群成员发送", pipelineIdentity(f.colleague), pipelineValue(groupID), pipelineAccepted, pipelineAccepted},
			{"同工作区非群成员", pipelineIdentity(f.handlerless), pipelineValue(groupID), notFound, notFound},
			{"其他工作区成员", pipelineIdentity(f.outsider), pipelineValue(groupID), notFound, notFound},
			{"被移出的成员", pipelineIdentity(f.colleague), func() string {
				_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, coordinator).Execute(f.ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: removed, MemberIdentityID: f.colleague.WorkspaceIdentity.ID})
				require.NoError(t, err)
				return removed
			}, notFound, notFound},
			{"已退群的成员", pipelineIdentity(f.colleague), func() string {
				require.NoError(t, groupchataction.NewLeaveGroupConversationAction(f.db, testEnqueuer, coordinator).Execute(f.ctx, f.colleague, left))
				return left
			}, notFound, notFound},
			{"群已解散", pipelineIdentity(f.owner), func() string {
				_, err := groupchataction.NewDissolveGroupConversationAction(f.db, coordinator).Execute(f.ctx, f.owner, dissolved)
				require.NoError(t, err)
				return dissolved
			}, notFound, notFound},
			{"群已解散时原成员发送", pipelineIdentity(f.colleague), pipelineValue(dissolved), notFound, notFound},
			{"个人归档后发送", pipelineIdentity(f.colleague), func() string {
				require.NoError(t, conversationaction.NewUpdateConversationArchiveAction(f.db).Execute(f.ctx, f.colleague, archived, true))
				return archived
			}, pipelineAccepted, pipelineAccepted},
		})
		pipelineRequireArchiveCleared(t, f, archived)
	})

	t.Run("AI 聊天", func(t *testing.T) {
		conversationID := f.agentConversation(f.agent.IdentityID)
		runPipelineCases(t, f, kinds["agent"], []pipelineCase{
			{"发起成员发送", pipelineIdentity(f.owner), pipelineValue(conversationID), pipelineAccepted, pipelineAccepted},
			{"同工作区其他成员", pipelineIdentity(f.colleague), pipelineValue(conversationID), notFound, notFound},
			{"其他工作区成员", pipelineIdentity(f.outsider), pipelineValue(conversationID), notFound, notFound},
			{"AI 员工停用", pipelineIdentity(f.owner), func() string {
				update := agentaction.NewUpdateStatusAction(f.db, testEnqueuer, testServiceSessionReturner(f.db))
				_, err := update.Execute(f.ctx, f.owner, f.agent.ID, domain.IdentityStatusInactive)
				require.NoError(t, err)
				t.Cleanup(func() { _, _ = update.Execute(context.Background(), f.owner, f.agent.ID, domain.IdentityStatusActive) })
				return conversationID
			}, notFound, notFound},
		})
	})

	t.Run("个人 AI 员工聊天", func(t *testing.T) {
		registered, err := computeraction.NewRegisterComputerAction(f.db).Execute(f.ctx, f.owner, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "流水线电脑"})
		require.NoError(t, err)
		personal, err := agentaction.NewCreatePersonalAgentAction(f.db).Execute(f.ctx, f.owner, registered.Record.ID, agentaction.PersonalAgentInput{
			DisplayName: "流水线个人助手", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: f.modelID}},
		})
		require.NoError(t, err)
		conversationID := f.agentConversation(personal.IdentityID)
		runPipelineCases(t, f, kinds["agent"], []pipelineCase{
			{"负责人发送", pipelineIdentity(f.owner), pipelineValue(conversationID), pipelineAccepted, pipelineAccepted},
			{"个人 AI 员工暂停", pipelineIdentity(f.owner), func() string {
				_, err := agentaction.NewSetPersonalAgentPausedAction(f.db).Execute(f.ctx, f.owner, personal.ID, true)
				require.NoError(t, err)
				return conversationID
			}, pipelineConflict(conversationaction.ConflictReasonPersonalAgentPaused), pipelineConflict(conversationaction.ConflictReasonPersonalAgentPaused)},
			{"电脑撤销后未绑定", pipelineIdentity(f.owner), func() string {
				require.NoError(t, computeraction.NewRevokeComputerAction(f.db, newTestToolDecisions(f.db, testEnqueuer)).Execute(f.ctx, f.owner, registered.Record.ID))
				return conversationID
			}, pipelineConflict(conversationaction.ConflictReasonPersonalAgentUnbound), pipelineConflict(conversationaction.ConflictReasonPersonalAgentUnbound)},
		})
	})

	t.Run("客服服务会话", func(t *testing.T) {
		conversationID := f.serviceConversation()
		closed := f.serviceConversation()
		closer := servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer)
		runPipelineCases(t, f, kinds["service"], []pipelineCase{
			{"未分配周期首个接待成员发送", pipelineIdentity(f.owner), pipelineValue(conversationID), pipelineAccepted, pipelineAccepted},
			{"负责人以外的接待成员", pipelineIdentity(f.colleague), pipelineValue(conversationID),
				pipelineConflict(conversationaction.ConflictReasonServiceSessionOwned), pipelineConflict(conversationaction.ConflictReasonServiceSessionOwned)},
			{"未开启接待的成员", pipelineIdentity(f.handlerless), pipelineValue(conversationID),
				pipelineConflict(servicesessionaction.ConflictReasonServiceHandlingRequired), pipelineConflict(servicesessionaction.ConflictReasonServiceHandlingRequired)},
			{"其他工作区成员", pipelineIdentity(f.outsider), pipelineValue(conversationID), notFound, notFound},
			{"服务周期已关闭", pipelineIdentity(f.owner), func() string {
				_, err := closer.Execute(f.ctx, f.owner, closed)
				require.NoError(t, err)
				return closed
			}, pipelineConflict(conversationaction.ConflictReasonServiceSessionNotReplyable), pipelineConflict(conversationaction.ConflictReasonServiceSessionNotReplyable)},
		})
	})

	t.Run("Copilot 线程", func(t *testing.T) {
		threadID := f.copilotThread()
		runPipelineCases(t, f, kinds["copilot"], []pipelineCase{
			{"线程创建者发送", pipelineIdentity(f.owner), pipelineValue(threadID), pipelineAccepted, pipelineAccepted},
			{"同工作区接待成员首次提问", pipelineIdentity(f.colleague), pipelineValue(threadID), pipelineAccepted, pipelineAccepted},
			// Copilot 线程对工作区全部成员开放提问，未开启接待的成员同样可以提问。
			{"同工作区未开启接待的成员", pipelineIdentity(f.handlerless), pipelineValue(threadID), pipelineAccepted, pipelineAccepted},
			{"其他工作区成员", pipelineIdentity(f.outsider), pipelineValue(threadID), notFound, notFound},
			{"Copilot AI 员工停用", pipelineIdentity(f.owner), func() string {
				update := agentaction.NewUpdateStatusAction(f.db, testEnqueuer, testServiceSessionReturner(f.db))
				_, err := update.Execute(f.ctx, f.owner, f.copilot.ID, domain.IdentityStatusInactive)
				require.NoError(t, err)
				t.Cleanup(func() {
					_, _ = update.Execute(context.Background(), f.owner, f.copilot.ID, domain.IdentityStatusActive)
				})
				return threadID
			}, pipelineRejected(conversationaction.ErrAgentUnavailable), pipelineRejected(conversationaction.ErrAgentUnavailable)},
		})
	})
}

// pipelineRequireArchiveCleared 断言会话内全部成员的个人归档已被新消息清除。
func pipelineRequireArchiveCleared(t *testing.T, f *pipelineFixture, conversationID string) {
	t.Helper()
	archived, err := f.db.NewSelect().Model((*servermodels.ConversationUserState)(nil)).
		Where("conversation_id = ? AND archived_at IS NOT NULL", conversationID).Count(f.ctx)
	require.NoError(t, err)
	require.Zero(t, archived, "archive not cleared by new message")
}

// TestMemberSendPipelineReplayAfterRevocation 验证发送资格失效后以原发送编号重放，各会话类型都返回原消息。
func TestMemberSendPipelineReplayAfterRevocation(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture(t)
	kinds := f.kinds()
	for _, scenario := range []struct {
		name    string
		kind    string
		prepare func(t *testing.T) (*servermodels.Identity, string)
		revoke  func(t *testing.T, conversationID string)
	}{
		{"单聊对端停用", "direct", func(t *testing.T) (*servermodels.Identity, string) {
			peer := newChatLockUser(t, f.db, f.owner)
			return f.owner, f.directConversation(f.owner, peer)
		}, func(t *testing.T, conversationID string) {
			var peerUserID string
			require.NoError(t, f.db.NewSelect().TableExpr("direct_conversations AS dc").ColumnExpr("u.id").
				Join("JOIN users AS u ON u.identity_id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", f.owner.WorkspaceIdentity.ID).
				Where("dc.conversation_id = ?", conversationID).Scan(f.ctx, &peerUserID))
			_, err := testUserStatusAction(f.db).Execute(f.ctx, f.owner, peerUserID, domain.IdentityStatusInactive)
			require.NoError(t, err)
		}},
		{"群已解散", "group", func(t *testing.T) (*servermodels.Identity, string) {
			return f.owner, f.groupConversation(f.colleague)
		}, func(t *testing.T, conversationID string) {
			_, err := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(f.ctx, f.owner, conversationID)
			require.NoError(t, err)
		}},
		{"服务周期已关闭", "service", func(t *testing.T) (*servermodels.Identity, string) {
			return f.owner, f.serviceConversation()
		}, func(t *testing.T, conversationID string) {
			_, err := servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(f.ctx, f.owner, conversationID)
			require.NoError(t, err)
		}},
		{"Copilot AI 员工停用", "copilot", func(t *testing.T) (*servermodels.Identity, string) {
			return f.owner, f.copilotThread()
		}, func(t *testing.T, _ string) {
			update := agentaction.NewUpdateStatusAction(f.db, testEnqueuer, testServiceSessionReturner(f.db))
			_, err := update.Execute(f.ctx, f.owner, f.copilot.ID, domain.IdentityStatusInactive)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = update.Execute(context.Background(), f.owner, f.copilot.ID, domain.IdentityStatusActive)
			})
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			kind := kinds[scenario.kind]
			sender, conversationID := scenario.prepare(t)
			textID, attachmentID, fileID := uuid.NewV7().String(), uuid.NewV7().String(), f.attachmentFile(sender)
			text, err := kind.text(sender, conversationID, textID, "重放文本", "")
			require.NoError(t, err)
			attachment, err := kind.attachment(sender, conversationID, attachmentID, fileID, "重放附件")
			require.NoError(t, err)
			scenario.revoke(t, conversationID)
			replayedText, err := kind.text(sender, conversationID, textID, "重放文本", "")
			require.NoError(t, err, "text replay")
			require.Equal(t, text.ID, replayedText.ID)
			replayedAttachment, err := kind.attachment(sender, conversationID, attachmentID, fileID, "重放附件")
			require.NoError(t, err, "attachment replay")
			require.Equal(t, attachment.ID, replayedAttachment.ID)
		})
	}
}

// TestMemberSendPipelineFirstDirectReplayAfterRevocation 验证向单聊目标首发的文本与附件在对端停用后以原发送编号重放，返回原消息与单聊摘要。
func TestMemberSendPipelineFirstDirectReplayAfterRevocation(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture(t)
	peer := newChatLockUser(t, f.db, f.owner)
	textInput := directchataction.FirstDirectTextMessageInput{TargetIdentityID: peer.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "首发文本"}
	sendText := directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer)
	text, err := sendText.Execute(f.ctx, f.owner, textInput)
	require.NoError(t, err)
	attachmentInput := directchataction.AttachmentMessageInput{TargetIdentityID: peer.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), FileID: f.attachmentFile(f.owner), Body: "首发附件"}
	sendAttachment := directchataction.NewSendAttachmentMessageAction(f.db, testEnqueuer, f.scheduler)
	attachment, err := sendAttachment.Execute(f.ctx, f.owner, attachmentInput)
	require.NoError(t, err)

	_, err = testUserStatusAction(f.db).Execute(f.ctx, f.owner, peer.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	_, err = sendText.Execute(f.ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: peer.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "新消息"})
	require.ErrorIs(t, err, conversationaction.ErrDirectTargetNotFound)

	replayedText, err := sendText.Execute(f.ctx, f.owner, textInput)
	require.NoError(t, err)
	require.Equal(t, text.Message.ID, replayedText.Message.ID)
	require.Equal(t, text.Conversation.ID, replayedText.Conversation.ID)
	require.Equal(t, peer.WorkspaceIdentity.ID, replayedText.Conversation.PeerIdentityID)
	replayedAttachment, err := sendAttachment.Execute(f.ctx, f.owner, attachmentInput)
	require.NoError(t, err)
	require.Equal(t, attachment.Message.ID, replayedAttachment.Message.ID)
	require.NotNil(t, replayedAttachment.Conversation)
	require.Equal(t, text.Conversation.ID, replayedAttachment.ConversationID)
}

// pipelineConversation 为指定会话类型建立一个 owner 可发送的新会话。
func (f *pipelineFixture) pipelineConversation(kind string) string {
	switch kind {
	case "direct":
		return f.directConversation(f.owner, f.colleague)
	case "group":
		return f.groupConversation(f.colleague)
	case "agent":
		return f.agentConversation(f.agent.IdentityID)
	case "service":
		return f.serviceConversation()
	default:
		return f.copilotThread()
	}
}

// TestMemberSendPipelineIdempotency 验证五种会话类型上文本与附件按发送编号并发收敛为一条消息、重放返回原消息、改变正文的重放被拒绝。
func TestMemberSendPipelineIdempotency(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture(t)
	for _, kind := range []string{"direct", "group", "agent", "service", "copilot"} {
		for _, payload := range []string{"text", "attachment"} {
			t.Run(kind+"/"+payload, func(t *testing.T) {
				entry := f.kinds()[kind]
				conversationID := f.pipelineConversation(kind)
				clientID, fileID := uuid.NewV7().String(), ""
				if payload == "attachment" {
					fileID = f.attachmentFile(f.owner)
				}
				send := func(body string) (conversationaction.ConversationMessage, error) {
					if payload == "text" {
						return entry.text(f.owner, conversationID, clientID, body, "")
					}
					return entry.attachment(f.owner, conversationID, clientID, fileID, body)
				}
				// 同一发送编号并发发送。
				results := make([]conversationaction.ConversationMessage, 4)
				failures := make([]error, len(results))
				start := make(chan struct{})
				var wg sync.WaitGroup
				for i := range results {
					wg.Go(func() {
						<-start
						results[i], failures[i] = send("幂等正文")
					})
				}
				close(start)
				wg.Wait()
				for i := range results {
					require.NoError(t, failures[i], "concurrent %d", i)
					require.Equal(t, results[0].ID, results[i].ID, "concurrent %d", i)
					require.Equal(t, &clientID, results[i].ClientMessageID, "concurrent %d", i)
				}
				count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ? AND client_message_id = ?", conversationID, clientID).Count(f.ctx)
				require.NoError(t, err)
				require.Equal(t, int64(1), count, "stored messages")
				replayed, err := send("幂等正文")
				require.NoError(t, err)
				require.Equal(t, results[0].ID, replayed.ID, "replay")
				require.Equal(t, results[0].MessageSeq, replayed.MessageSeq, "replay seq")
				_, err = send("改变后的正文")
				pipelineConflict(conversationaction.ConflictReasonIdempotencyMismatch)(t, err, "changed body")
			})
		}
	}
}

// TestMemberSendPipelineConsistency 验证五种会话类型上文本与附件发送对消息序号、会话最新消息、发送者阅读水位与对方未读数的更新一致，跨会话引用被拒绝。
func TestMemberSendPipelineConsistency(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture(t)
	other := f.directConversation(f.owner, f.handlerless)
	foreign, err := f.kinds()["direct"].text(f.owner, other, uuid.NewV7().String(), "其他会话消息", "")
	require.NoError(t, err)
	for _, kind := range []string{"direct", "group", "agent", "service", "copilot"} {
		t.Run(kind, func(t *testing.T) {
			entry := f.kinds()[kind]
			conversationID := f.pipelineConversation(kind)
			peerUnread := func() int {
				if kind != "direct" && kind != "group" {
					return 0
				}
				details, err := inboxaction.NewLoadInboxQuery(f.db).ReadByIDs(f.ctx, f.colleague, []string{conversationID}, &inboxaction.LoadInput{Scope: domain.InboxScopeChat})
				require.NoError(t, err)
				require.Len(t, details, 1)
				require.NotNil(t, details[0].Conversation)
				return details[0].Conversation.UnreadCount
			}
			before := peerUnread()
			text, err := entry.text(f.owner, conversationID, uuid.NewV7().String(), "一致性文本", "")
			require.NoError(t, err)
			attachment, err := entry.attachment(f.owner, conversationID, uuid.NewV7().String(), f.attachmentFile(f.owner), "一致性附件")
			require.NoError(t, err)
			require.Equal(t, domain.MessageTypeText, text.Type)
			require.Equal(t, domain.MessageTypeAttachment, attachment.Type)
			require.NotNil(t, attachment.Attachment)
			require.Equal(t, text.MessageSeq+1, attachment.MessageSeq, "consecutive seq")
			stored := &servermodels.Conversation{ID: conversationID}
			require.NoError(t, f.db.NewSelect().Model(stored).WherePK().Scan(f.ctx))
			require.Equal(t, &attachment.ID, stored.LastMessageID, "last message")
			require.Equal(t, attachment.MessageSeq, stored.LastMessageSeq, "last seq")
			var states []servermodels.ConversationUserState
			require.NoError(t, f.db.NewSelect().Model(&states).Where("cus.conversation_id = ? AND cus.user_id = ?", conversationID, f.owner.User.ID).Scan(f.ctx))
			switch kind {
			case "copilot":
				// Copilot 线程不维护个人会话状态。
				require.Empty(t, states, "copilot user state")
			case "service":
				// 现状：客服会话发送不推进发送者的个人阅读水位，本人消息不计未读，访客首条消息仍算 1 条未读。
				require.Empty(t, states, "service user state")
				details, err := inboxaction.NewLoadInboxQuery(f.db).ReadByIDs(f.ctx, f.owner, []string{conversationID}, &inboxaction.LoadInput{Scope: domain.InboxScopeAll})
				require.NoError(t, err)
				require.Len(t, details, 1)
				require.NotNil(t, details[0].Conversation)
				require.Equal(t, 1, details[0].Conversation.UnreadCount, "service sender unread")
			default:
				require.Len(t, states, 1)
				require.Equal(t, &attachment.ID, states[0].LastReadMessageID, "sender read position")
			}
			if kind == "direct" || kind == "group" {
				require.Equal(t, before+2, peerUnread(), "peer unread")
			}
			// 引用其他会话的消息被拒绝且不写入消息。
			_, err = entry.text(f.owner, conversationID, uuid.NewV7().String(), "跨会话引用", foreign.ID)
			pipelineConflict(conversationaction.ConflictReasonReplyTargetInvalid)(t, err, "cross conversation reply")
			after := &servermodels.Conversation{ID: conversationID}
			require.NoError(t, f.db.NewSelect().Model(after).WherePK().Scan(f.ctx))
			require.Equal(t, stored.LastMessageSeq, after.LastMessageSeq, "rejected reply wrote message")
		})
	}
}
