//go:build server

// 本文件以表驱动矩阵锁定各类会话对各类身份在收件箱列表、按编号读取、消息历史、置顶、输入状态、文件区与消息通知入口上的读权限结论。
package integrationtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/usernotification"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// accessEntries 是一名身份对一个会话在各读入口上的结论：true 表示允许或可见。
type accessEntries struct {
	List    bool // 收件箱列表（内部会话取聊天范围，服务会话取全部服务会话并按周期状态筛选）。
	ByID    bool // 收件箱按编号读取摘要。
	History bool // 按会话读取消息历史。
	Pin     bool // 个人置顶。
	Typing  bool // 上报输入状态（发送侧资格）。
	Files   bool // 会话文件区列表。
}

// accessAll 表示全部入口允许。
var accessAll = accessEntries{List: true, ByID: true, History: true, Pin: true, Typing: true, Files: true}

// accessNone 表示全部入口拒绝。
var accessNone = accessEntries{}

// accessConversation 是矩阵中的一个会话及其在收件箱中的读取方式。
type accessConversation struct {
	name          string
	id            string
	serviceStatus domain.ServiceSessionStatus // 非空表示服务会话，列表按该周期状态筛选全部服务会话。
}

// accessMatrixFixture 是读权限矩阵共用的工作区、身份与会话。
type accessMatrixFixture struct {
	db            *bun.DB
	identities    map[string]*servermodels.Identity // 按矩阵身份名称索引。
	order         []string                          // 身份名称的固定顺序。
	files         *conversationfile.MemberFiles
	messages      map[string]string // 按会话名称索引用于通知矩阵的消息编号。
	conversations []accessConversation
}

// newAccessMatrixFixture 建立一个工作区：所有者、单聊双方、未参与成员、被移出群成员、主动退群成员，另建其他工作区成员，并创建各类会话。
func newAccessMatrixFixture(t *testing.T) *accessMatrixFixture {
	t.Helper()
	ctx := context.Background()
	db, owner, _, modelID := newAIWorkspace(t)
	f := &accessMatrixFixture{db: db, identities: map[string]*servermodels.Identity{"owner": owner}, messages: map[string]string{}}
	for _, name := range []string{"memberA", "memberB", "outsider", "removed", "left"} {
		f.identities[name] = newChatLockUser(t, db, owner)
	}
	f.identities["otherWorkspace"] = newNavigationFixture(t).owner
	f.order = []string{"owner", "memberA", "memberB", "outsider", "removed", "left", "otherWorkspace"}
	disableAutoAssignment(t, db, owner.Workspace.ID)
	local, err := serverfilecontent.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	settings := func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} }
	f.files = conversationfile.NewMemberFiles(conversationfile.NewStore(db, func() domain.FileStorageBackend { return domain.FileStorageBackendLocal },
		serverfilecontent.NewWriter(local, settings), serverfilecontent.NewReader(local, settings)))
	a, b := f.identities["memberA"], f.identities["memberB"]
	removed, left := f.identities["removed"], f.identities["left"]

	// 成员单聊：memberA 向 memberB 发起。
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(db, testEnqueuer).Execute(ctx, a, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: b.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊消息",
	})
	require.NoError(t, err)
	f.add("direct", direct.Conversation.ID, "")
	f.messages["direct"] = direct.Message.ID

	// 活跃群聊：所有者建群，移出 removed，left 主动退群，之后所有者发言。
	group := f.createGroup(t, "活跃群", a, removed, left)
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, newGroupAgentCoordinator(db)).Execute(ctx, owner, groupchataction.GroupConversationMemberInput{ConversationID: group, MemberIdentityID: removed.WorkspaceIdentity.ID})
	require.NoError(t, err)
	require.NoError(t, groupchataction.NewLeaveGroupConversationAction(db, testEnqueuer, newGroupAgentCoordinator(db)).Execute(ctx, left, group))
	groupMessage, err := newGroupSendAction(db).Execute(ctx, owner, groupchataction.GroupTextMessageInput{ConversationID: group, ClientMessageID: uuid.NewV7().String(), Body: "群消息"})
	require.NoError(t, err)
	f.add("group", group, "")
	f.messages["group"] = groupMessage.ID

	// 已解散群聊：所有者建群并发言，移出 removed 后解散。
	dissolved := f.createGroup(t, "解散群", a, removed)
	dissolvedMessage, err := newGroupSendAction(db).Execute(ctx, owner, groupchataction.GroupTextMessageInput{ConversationID: dissolved, ClientMessageID: uuid.NewV7().String(), Body: "解散前消息"})
	require.NoError(t, err)
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, newGroupAgentCoordinator(db)).Execute(ctx, owner, groupchataction.GroupConversationMemberInput{ConversationID: dissolved, MemberIdentityID: removed.WorkspaceIdentity.ID})
	require.NoError(t, err)
	_, err = groupchataction.NewDissolveGroupConversationAction(db, newGroupAgentCoordinator(db)).Execute(ctx, owner, dissolved)
	require.NoError(t, err)
	f.add("dissolvedGroup", dissolved, "")
	f.messages["dissolvedGroup"] = dissolvedMessage.ID

	// 网站客服会话：排队中、memberA 领取、已关闭三种周期。
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "读权限矩阵", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{})
	visitor := func(conversationID *string) customerchataction.ReceiveWebsiteCustomerMessageResult {
		externalID := "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
		if conversationID != nil {
			var existing string
			require.NoError(t, db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("ci.external_id").
				Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").Where("cc.conversation_id = ?", *conversationID).Scan(ctx, &existing))
			externalID = existing
		}
		result, err := receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: channel.ID, ExternalID: externalID, ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "访客消息",
		})
		require.NoError(t, err)
		return result
	}
	coordinator := newTestAgentRun(db, nil, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	claim := servicesessionaction.NewClaimServiceSessionAction(db, coordinator, testEnqueuer)
	queued := visitor(nil)
	f.add("serviceQueued", queued.Conversation.ID, domain.ServiceSessionStatusOpen)
	f.messages["serviceQueued"] = queued.Message.ID
	assigned := visitor(nil)
	_, err = claim.Execute(ctx, a, assigned.Conversation.ID)
	require.NoError(t, err)
	assignedID := assigned.Conversation.ID
	assignedMessage := visitor(&assignedID)
	f.add("serviceAssigned", assignedID, domain.ServiceSessionStatusOpen)
	f.messages["serviceAssigned"] = assignedMessage.Message.ID
	closed := visitor(nil)
	_, err = claim.Execute(ctx, a, closed.Conversation.ID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(db, coordinator, testEnqueuer).Execute(ctx, a, closed.Conversation.ID)
	require.NoError(t, err)
	f.add("serviceClosed", closed.Conversation.ID, domain.ServiceSessionStatusClosed)
	f.messages["serviceClosed"] = closed.Message.ID

	// AI 员工会话与副驾驶线程：同一 AI 员工服务客户，memberA 与其单聊，所有者在排队客服会话上开副驾驶线程。
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "回答问题"}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{
		DisplayName: "矩阵助手", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, Execution: execution,
	})
	require.NoError(t, err)
	agentChat, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, a, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "AI 会话消息",
	})
	require.NoError(t, err)
	f.add("agentChat", agentChat.Conversation.ID, "")
	f.messages["agentChat"] = agentChat.Message.ID
	copilot, err := directchataction.NewSendFirstServiceCopilotMessageAction(db, testEnqueuer, scheduler).Execute(ctx, owner, directchataction.FirstServiceCopilotMessageInput{
		ThreadID: uuid.NewV7().String(), ServedConversationID: queued.Conversation.ID, AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "副驾驶提问",
	})
	require.NoError(t, err)
	f.add("copilotThread", copilot.Thread.ID, "")
	f.messages["copilotThread"] = copilot.Message.ID

	// 员工服务会话：服务员工的 AI 员工接待 memberB 的提问。
	employeeAgent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{
		DisplayName: "服务台", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceEmployee}, Execution: execution,
	})
	require.NoError(t, err)
	employee, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, b, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: employeeAgent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "电脑坏了",
	})
	require.NoError(t, err)
	f.add("employeeService", employee.Conversation.ID, domain.ServiceSessionStatusOpen)
	f.messages["employeeService"] = employee.Message.ID
	return f
}

// add 登记矩阵会话。
func (f *accessMatrixFixture) add(name, id string, status domain.ServiceSessionStatus) {
	f.conversations = append(f.conversations, accessConversation{name: name, id: id, serviceStatus: status})
}

// createGroup 由所有者创建含指定成员的群聊。
func (f *accessMatrixFixture) createGroup(t *testing.T, title string, members ...*servermodels.Identity) string {
	t.Helper()
	ids := arr.Map(members, func(member *servermodels.Identity) string { return member.WorkspaceIdentity.ID })
	group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(context.Background(), f.identities["owner"], groupchataction.GroupConversationInput{Title: title, MemberIdentityIDs: ids})
	require.NoError(t, err)
	return group.ID
}

// accessAllowed 把入口返回转成结论：成功为 true，会话不存在为 false，其余错误使测试失败。
func accessAllowed(t *testing.T, entry string, err error) bool {
	t.Helper()
	if err == nil {
		return true
	}
	require.True(t, errors.Is(err, conversationaction.ErrConversationNotFound), "%s 返回非预期错误：%v", entry, err)
	return false
}

// observe 依次调用各读入口并汇总结论。
func (f *accessMatrixFixture) observe(t *testing.T, identity *servermodels.Identity, conversation accessConversation) accessEntries {
	t.Helper()
	ctx := context.Background()
	inbox := inboxaction.NewLoadInboxQuery(f.db)
	var got accessEntries
	input := inboxaction.LoadInput{Scope: domain.InboxScopeChat}
	if conversation.serviceStatus != "" {
		input = inboxaction.LoadInput{Scope: domain.InboxScopeAll, ServiceStatus: conversation.serviceStatus}
	}
	page, _, err := inbox.Execute(ctx, identity, input)
	require.NoError(t, err)
	got.List = slices.ContainsFunc(page.Conversations, func(row inboxaction.ConversationSummary) bool { return row.ID == conversation.id })
	results, err := inbox.ReadByIDs(ctx, identity, []string{conversation.id}, nil)
	require.NoError(t, err)
	got.ByID = results[0].Conversation != nil
	_, err = conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversation.id})
	got.History = accessAllowed(t, "history", err)
	var version int64
	require.NoError(t, f.db.NewSelect().Table("users").Column("pin_order_version").Where("id = ?", identity.User.ID).Scan(ctx, &version))
	_, err = conversationaction.NewUpdateConversationPinAction(f.db).Execute(ctx, identity, conversationaction.ConversationPinInput{ConversationID: conversation.id, Pinned: true, ExpectedPinOrderVersion: version})
	got.Pin = accessAllowed(t, "pin", err)
	got.Typing = accessAllowed(t, "typing", conversationaction.NewReportConversationTypingAction(f.db).Execute(ctx, identity, conversation.id, true))
	_, err = f.files.List(ctx, identity, conversation.id)
	got.Files = accessAllowed(t, "files", err)
	return got
}

// TestConversationAccessMatrix 锁定会话类型 × 身份在各读入口上的结论。
func TestConversationAccessMatrix(t *testing.T) {
	t.Parallel()
	f := newAccessMatrixFixture(t)
	// 服务会话对全部工作区成员开放阅读，输入状态与对客发送资格一致：开启接待、周期开放且无人负责或由本人负责。
	serviceReader := accessEntries{List: true, ByID: true, History: true, Pin: true, Files: true}
	serviceTyper := accessAll
	// 已解散群保留在群成员的阅读资格，输入状态要求群聊仍活跃。
	dissolvedReader := accessEntries{List: true, ByID: true, History: true, Pin: true, Files: true}
	// 副驾驶线程对全部成员开放消息历史与文件区，不进入收件箱、按编号读不到摘要、也不能置顶，线程不提供输入状态。
	copilotReader := accessEntries{History: true, Files: true}
	expected := map[string]map[string]accessEntries{
		"direct":          {"memberA": accessAll, "memberB": accessAll},
		"group":           {"owner": accessAll, "memberA": accessAll},
		"dissolvedGroup":  {"owner": dissolvedReader, "memberA": dissolvedReader},
		"serviceQueued":   {"owner": serviceTyper, "memberA": serviceTyper, "memberB": serviceTyper, "outsider": serviceTyper, "removed": serviceTyper, "left": serviceTyper},
		"serviceAssigned": {"owner": serviceReader, "memberA": serviceTyper, "memberB": serviceReader, "outsider": serviceReader, "removed": serviceReader, "left": serviceReader},
		"serviceClosed":   {"owner": serviceReader, "memberA": serviceReader, "memberB": serviceReader, "outsider": serviceReader, "removed": serviceReader, "left": serviceReader},
		"agentChat":       {"memberA": accessAll},
		"copilotThread":   {"owner": copilotReader, "memberA": copilotReader, "memberB": copilotReader, "outsider": copilotReader, "removed": copilotReader, "left": copilotReader},
		"employeeService": {"owner": serviceReader, "memberA": serviceReader, "memberB": serviceReader, "outsider": serviceReader, "removed": serviceReader, "left": serviceReader},
	}
	for _, conversation := range f.conversations {
		t.Run(conversation.name, func(t *testing.T) {
			want := make(map[string]accessEntries, len(f.order))
			got := make(map[string]accessEntries, len(f.order))
			for _, name := range f.order {
				want[name] = accessNone
				if entries, ok := expected[conversation.name][name]; ok {
					want[name] = entries
				}
				got[name] = f.observe(t, f.identities[name], conversation)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("读权限矩阵不符 (-want +got):\n%s", diff)
			}
		})
	}
	t.Run("消息通知接收人", func(t *testing.T) {
		feed := startRealtimeFeed(t, f.identities["owner"].Workspace.ID)
		deliverer := usernotification.NewDeliverer(f.db, testEnqueuer)
		names := arr.Associate(f.order, func(name string) (string, string) { return f.identities[name].User.ID, name })
		// 各会话一条消息的通知接收人；发送者本人与已读该消息的成员不接收。
		want := map[string][]string{
			"direct":          {"memberB"},
			"group":           {"memberA"},
			"dissolvedGroup":  {"memberA"},
			"serviceQueued":   {"owner", "memberA", "memberB", "outsider", "removed", "left"},
			"serviceAssigned": {"memberA"},
			"serviceClosed":   {},
			"agentChat":       {},
			"copilotThread":   {},
			"employeeService": {},
		}
		got := make(map[string][]string, len(f.conversations))
		for _, conversation := range f.conversations {
			deliverMessage(t, deliverer, f.identities["owner"].Workspace.ID, conversation.id, f.messages[conversation.name])
			recipients := make([]string, 0)
			for _, notice := range feed.userNotices(t) {
				require.Equal(t, conversation.id, notice.ConversationID)
				recipients = append(recipients, names[notice.UserID])
			}
			slices.SortFunc(recipients, func(x, y string) int { return slices.Index(f.order, x) - slices.Index(f.order, y) })
			got[conversation.name] = recipients
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("通知接收人不符 (-want +got):\n%s", diff)
		}
	})
}
