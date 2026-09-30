//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	contactaction "github.com/runforyou-ai/cervi/internal/actions/contact"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	useraction "github.com/runforyou-ai/cervi/internal/actions/user"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// profileFixture 保存成员出现在单聊、群聊、已退出群聊和客户会话中的资料失效场景。
type profileFixture struct {
	customerReadFixture
	directID, leftGroupID string
}

// newProfileFixture 建立成员与群主的单聊、成员已退出的群聊，并由成员领取网站客户会话。
func newProfileFixture(t *testing.T) profileFixture {
	t.Helper()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊",
	})
	if err != nil {
		t.Fatal(err)
	}
	left, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "已退出的群", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newGroupSendAction(f.db).Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: left.ID, ClientMessageID: uuid.NewV7().String(), Body: "退出前的发言"}); err != nil {
		t.Fatal(err)
	}
	if err := groupchataction.NewLeaveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, left.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, newGroupAgentCoordinator(f.db), newTestTasks(f.db)).Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	return profileFixture{customerReadFixture: f, directID: direct.Conversation.ID, leftGroupID: left.ID}
}

// versions 读取成员资料所在四个会话的当前版本。
func (f profileFixture) versions(t *testing.T) map[string]int64 {
	t.Helper()
	versions := f.internalVersions(t)
	versions[f.conversationID] = loadConversationVersion(t, f.db, f.conversationID)
	return versions
}

// internalVersions 读取成员资料所在三个内部会话的当前版本。
func (f profileFixture) internalVersions(t *testing.T) map[string]int64 {
	t.Helper()
	return map[string]int64{
		f.groupID:     loadConversationVersion(t, f.db, f.groupID),
		f.directID:    loadConversationVersion(t, f.db, f.directID),
		f.leftGroupID: loadConversationVersion(t, f.db, f.leftGroupID),
	}
}

// expectVersions 断言基线中各会话的版本相对基线推进了指定次数。
func (f profileFixture) expectVersions(t *testing.T, step string, before map[string]int64, delta int64) {
	t.Helper()
	current := f.versions(t)
	for conversationID, version := range before {
		if current[conversationID] != version+delta {
			t.Fatalf("%s: 会话 %s 版本 = %d，want %d", step, conversationID, current[conversationID], version+delta)
		}
	}
}

// TestMemberProfileConversationInvalidation 验证成员名称、头像与账号状态实际变化时推进展示该成员的会话版本，已退出的成员不接收通知，相同值不推进。
func TestMemberProfileConversationInvalidation(t *testing.T) {
	t.Parallel()
	f := newProfileFixture(t)
	ctx := context.Background()
	// 群主恢复管理员角色，管理员修改成员与停用成员需要企业保留有效管理员。
	if _, err := f.db.NewUpdate().Table("users").
		Set("role_id = (SELECT id FROM roles WHERE organization_id = ? AND kind = ?)", f.owner.Organization.ID, domain.RoleKindAdmin).
		Where("id = ?", f.owner.User.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 成员只负责客户会话、尚未发言，客户会话仅经当前负责人关联到成员。
	participated, err := f.db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id").
		Where("cp.conversation_id = ? AND cs.source_id = ?", f.conversationID, f.member.OrganizationIdentity.ID).
		Exists(ctx)
	if err != nil || participated {
		t.Fatalf("成员已是客户会话参与者 participated=%v err=%v", participated, err)
	}
	feed := startRealtimeFeed(t, f.owner.Organization.ID)
	profile := useraction.NewUpdateProfileAction(f.db)
	before := f.versions(t)
	visitorIdentityID := loadChannelIdentityID(t, f.db, f.conversationID)
	// changedNotices 构造成员资料变化后四个会话的期望通知，网站客户会话同时通知访客目录受众，企业网站访客另收接待状态变化通知。
	changedNotices := func() []receivedNotification {
		current := f.versions(t)
		return []receivedNotification{
			feed.notice(f.owner.User.ID, realtime.KindConversationChanged, f.groupID, current[f.groupID]),
			feed.notice(f.member.User.ID, realtime.KindConversationChanged, f.groupID, current[f.groupID]),
			feed.notice(f.owner.User.ID, realtime.KindConversationChanged, f.directID, current[f.directID]),
			feed.notice(f.member.User.ID, realtime.KindConversationChanged, f.directID, current[f.directID]),
			feed.notice(f.owner.User.ID, realtime.KindConversationChanged, f.leftGroupID, current[f.leftGroupID]),
			feed.customerInbox(f.conversationID, current[f.conversationID]),
			feed.visitorDirectory(visitorIdentityID, f.conversationID, current[f.conversationID]),
			feed.reception(),
		}
	}

	// 本人改名：身份资料只通知本人，会话变化通知当前成员、客服共享受众与网站访客目录受众，已退出的成员不接收。
	if _, err := profile.Execute(ctx, f.member, useraction.ProfileInput{DisplayName: "成员新名", Email: f.member.Account.Email}); err != nil {
		t.Fatal(err)
	}
	f.expectVersions(t, "本人改名", before, 1)
	after := f.versions(t)
	feed.expect(t, append(changedNotices(), feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID)))...)

	// 本人只换头像同样推进。
	avatar, err := fileaction.NewCreateUploadAction(f.db).Execute(ctx, f.member, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeUserAvatar, FileName: "avatar.png", ContentType: "image/png", ByteSize: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := markFileUploaded(ctx, f.db, f.member, avatar.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Execute(ctx, f.member, useraction.ProfileInput{DisplayName: "成员新名", Email: f.member.Account.Email, AvatarFileID: avatar.ID}); err != nil {
		t.Fatal(err)
	}
	f.expectVersions(t, "本人换头像", after, 1)
	after = f.versions(t)
	feed.expect(t, append(changedNotices(), feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID)))...)

	// 相同名称与仅邮箱变化都不推进会话版本。
	if _, err := profile.Execute(ctx, f.member, useraction.ProfileInput{DisplayName: "成员新名", Email: f.member.Account.Email}); err != nil {
		t.Fatal(err)
	}
	// 邮箱属于账号，本人修改邮箱推进身份资料版本但不推进会话版本。
	renamedEmail := uniqueEmail("profile-renamed")
	if _, err := profile.Execute(ctx, f.member, useraction.ProfileInput{DisplayName: "成员新名", Email: renamedEmail}); err != nil {
		t.Fatal(err)
	}
	f.member.Account.Email = renamedEmail
	f.expectVersions(t, "名称未变", after, 0)
	feed.expect(t, feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID)))

	// 管理员改名同样推进。
	if _, err := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), newTestTasks(f.db)).Execute(ctx, f.owner, f.member.User.ID, useraction.UpdateInput{DisplayName: "管理员改的名", RoleID: f.member.User.RoleID, HandlesServiceRequests: true, MaxServiceSessions: 10}); err != nil {
		t.Fatal(err)
	}
	f.expectVersions(t, "管理员改名", after, 1)
	feed.expect(t, append(changedNotices(), feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID)))...)

	// 停用与恢复改变列表展示的账号状态，重复停用不推进；停用另发连接撤销控制，本段只核对版本。
	status := testUserStatusAction(f.db)
	// 停用把该成员负责的客服周期退回公共队列，客户会话随退回事件推进一次后不再展示该成员。
	before = f.internalVersions(t)
	customerBefore := loadConversationVersion(t, f.db, f.conversationID)
	for _, step := range []struct {
		status domain.IdentityStatus
		delta  int64
	}{{domain.IdentityStatusInactive, 1}, {domain.IdentityStatusInactive, 1}, {domain.IdentityStatusActive, 2}} {
		if _, err := status.Execute(ctx, f.owner, f.member.User.ID, step.status); err != nil {
			t.Fatal(err)
		}
		f.expectVersions(t, "账号状态 "+string(step.status), before, step.delta)
	}
	if version := loadConversationVersion(t, f.db, f.conversationID); version != customerBefore+1 {
		t.Fatalf("停用后客户会话版本 = %d，want %d", version, customerBefore+1)
	}
	returnedSession := servermodels.ServiceSession{}
	if err := f.db.NewSelect().Model(&returnedSession).Where("ss.conversation_id = ?", f.conversationID).Scan(ctx); err != nil ||
		returnedSession.AssigneeIdentityID != nil || returnedSession.TeamID != nil {
		t.Fatalf("停用后客服周期 = %+v, error = %v", returnedSession, err)
	}
	events := returnedEvents(t, f.db, f.conversationID)
	if len(events) != 1 || events[0].FromIdentityID != f.member.OrganizationIdentity.ID ||
		events[0].Reason != domain.ServiceSessionReturnAssigneeUnavailable || events[0].Target.Kind != domain.ServiceSessionTargetPublicQueue {
		t.Fatalf("停用后退回事件 = %+v", events)
	}
	// 真人退回队列不向客户发送通知。
	notices, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).
		Where("msg.conversation_id = ? AND msg.idempotency_key LIKE ?", f.conversationID, "returned:%").
		Where("msg.type = ?", domain.MessageTypeText).Count(ctx)
	if err != nil || notices != 0 {
		t.Fatalf("停用后对客通知 = %d, error = %v", notices, err)
	}
}

// TestAgentProfileConversationInvalidation 验证带头像创建的 AI 员工改名、换头像与 Copilot 创建人的资料变化推进 Agent 聊天、Copilot 线程及其所属客户会话的版本，只改工作状态或重复停用不推进。
func TestAgentProfileConversationInvalidation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	provider := &servermodels.AIProvider{
		OrganizationID: f.owner.Organization.ID, Brand: string(domain.AIProviderBrandOpenAI), Name: "资料失效测试模型服务",
		CredentialType: string(domain.AIProviderCredentialTypeAPIKey), APIKey: "test-key", APIURL: "https://example.com/v1",
	}
	if _, err := f.db.NewInsert().Model(provider).Column("organization_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	model := &servermodels.AIProviderModel{
		ProviderID: provider.ID, OrganizationID: f.owner.Organization.ID, Identifier: "chat-model", Name: "测试对话模型", Type: string(domain.AIModelTypeChat),
		InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096,
	}
	if _, err := f.db.NewInsert().Model(model).Column("provider_id", "organization_id", "identifier", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// uploadAvatar 上传一张待关联的 AI 员工头像。
	uploadAvatar := func() string {
		file, err := fileaction.NewCreateUploadAction(f.db).Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeAgentAvatar, FileName: "agent.png", ContentType: "image/png", ByteSize: 1024,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := markFileUploaded(ctx, f.db, f.owner, file.ID, ""); err != nil {
			t.Fatal(err)
		}
		return file.ID
	}
	createdAvatarID := uploadAvatar()
	created, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "资料助手", AvatarFileID: createdAvatarID,
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: provider.ID, ModelIdentifier: model.Identifier, SystemInstruction: "回答问题"}},
	})
	if err != nil || created.AvatarFileID == nil || *created.AvatarFileID != createdAvatarID {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	tasks := newTestTasks(f.db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduler := agentrunaction.NewScheduler(tasks)
	chat, err := directchataction.NewSendFirstAgentTextMessageAction(f.db, scheduler).Execute(ctx, f.owner, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "你好",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 成员在客户会话中与 AI 员工开启 Copilot 线程；成员不参与客户会话，只以线程创建人出现在线程列表中。
	threadID := uuid.NewV7().String()
	if _, err := directchataction.NewSendFirstServiceCopilotMessageAction(f.db, scheduler).Execute(ctx, f.member, directchataction.FirstServiceCopilotMessageInput{
		ThreadID: threadID, ServedConversationID: f.conversationID, AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "帮我看看",
	}); err != nil {
		t.Fatal(err)
	}
	conversations := []string{chat.Conversation.ID, f.conversationID, threadID}
	visitorIdentityID := loadChannelIdentityID(t, f.db, f.conversationID)
	// versions 读取 Agent 聊天、客户会话与 Copilot 线程的当前版本。
	versions := func() []int64 {
		values := make([]int64, 0, len(conversations))
		for _, conversationID := range conversations {
			values = append(values, loadConversationVersion(t, f.db, conversationID))
		}
		return values
	}
	feed := startRealtimeFeed(t, f.owner.Organization.ID)
	update := agentaction.NewUpdateAgentAction(f.db, testServiceSessionReturner(f.db))
	status := agentaction.NewUpdateStatusAction(f.db, testServiceSessionReturner(f.db))
	rename := func(name string, workStatus domain.WorkStatus) error {
		_, err := update.Execute(ctx, f.owner, created.ID, agentaction.UpdateInput{DisplayName: name, ServiceAudiences: created.ServiceAudiences, WorkStatus: workStatus})
		return err
	}
	firstAvatarID, secondAvatarID := uploadAvatar(), uploadAvatar()
	// changeAvatar 保持名称与工作状态，只提交头像。
	changeAvatar := func(fileID string) error {
		_, err := update.Execute(ctx, f.owner, created.ID, agentaction.UpdateInput{DisplayName: "资料助手新名", ServiceAudiences: created.ServiceAudiences, WorkStatus: domain.WorkStatusAway, AvatarFileID: fileID})
		return err
	}
	for _, step := range []struct {
		name   string
		change func() error
		deltas []int64
	}{
		{"改名", func() error { return rename("资料助手新名", domain.WorkStatusWorking) }, []int64{1, 1, 1}},
		{"只改工作状态", func() error { return rename("资料助手新名", domain.WorkStatusAway) }, []int64{0, 0, 0}},
		{"换头像", func() error { return changeAvatar(firstAvatarID) }, []int64{1, 1, 1}},
		{"提交相同头像", func() error { return changeAvatar(firstAvatarID) }, []int64{0, 0, 0}},
		{"替换头像", func() error { return changeAvatar(secondAvatarID) }, []int64{1, 1, 1}},
		{"Copilot 创建人改名", func() error {
			_, err := useraction.NewUpdateProfileAction(f.db).Execute(ctx, f.member, useraction.ProfileInput{DisplayName: "线程创建人", Email: f.member.Account.Email})
			return err
		}, []int64{0, 1, 1}},
		// 删除 Agent 聊天中 AI 员工的参与记录，只保留其运行记录。
		{"仅有运行记录时改名", func() error {
			if _, err := f.db.NewDelete().TableExpr("conversation_participants AS cp").
				Where("cp.conversation_id = ?", chat.Conversation.ID).
				Where("cp.subject_id IN (SELECT id FROM chat_subjects WHERE source_id = ?)", created.IdentityID).
				Exec(ctx); err != nil {
				return err
			}
			return rename("只剩运行记录", domain.WorkStatusAway)
		}, []int64{1, 1, 1}},
		{"停用", func() error {
			_, err := status.Execute(ctx, f.owner, created.ID, domain.IdentityStatusInactive)
			return err
		}, []int64{1, 1, 1}},
		{"重复停用", func() error {
			_, err := status.Execute(ctx, f.owner, created.ID, domain.IdentityStatusInactive)
			return err
		}, []int64{0, 0, 0}},
	} {
		before := versions()
		if err := step.change(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		after := versions()
		var want []receivedNotification
		for index, delta := range step.deltas {
			if after[index] != before[index]+delta {
				t.Fatalf("%s: 会话 %s 版本 = %d，want %d", step.name, conversations[index], after[index], before[index]+delta)
			}
			if delta == 0 {
				continue
			}
			// Agent 聊天通知当前真人成员，客户会话与 Copilot 线程各以自身会话 ID 通知客服共享受众，网站客户会话另通知访客目录受众。
			switch index {
			case 0:
				want = append(want, feed.notice(f.owner.User.ID, realtime.KindConversationChanged, conversations[index], after[index]))
			case 1:
				want = append(want, feed.customerInbox(conversations[index], after[index]), feed.visitorDirectory(visitorIdentityID, conversations[index], after[index]))
			default:
				want = append(want, feed.customerInbox(conversations[index], after[index]))
			}
		}
		// 创建人另在夹具群中，改名同时通知群成员与本人身份资料。
		if step.name == "Copilot 创建人改名" {
			groupVersion := loadConversationVersion(t, f.db, f.groupID)
			want = append(want,
				feed.notice(f.owner.User.ID, realtime.KindConversationChanged, f.groupID, groupVersion),
				feed.notice(f.member.User.ID, realtime.KindConversationChanged, f.groupID, groupVersion),
				feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID)),
			)
		}
		// 名称、头像或账号状态实际变化时，企业网站访客另收接待状态变化通知。
		if len(want) > 0 {
			feed.expect(t, append(want, feed.reception())...)
		}
	}
	// 替换后的头像保持关联，被替换的旧头像交给清理任务。
	agent, err := agentaction.NewGetAgentQuery(f.db).Execute(ctx, f.owner, created.ID)
	if err != nil || agent.AvatarFileID == nil || *agent.AvatarFileID != secondAvatarID {
		t.Fatalf("agent=%+v err=%v", agent, err)
	}
	for fileID, want := range map[string]domain.FileStatus{createdAvatarID: domain.FileStatusDeleting, firstAvatarID: domain.FileStatusDeleting, secondAvatarID: domain.FileStatusActive} {
		var status string
		if err := f.db.NewSelect().TableExpr("files").Column("status").Where("id = ?", fileID).Scan(ctx, &status); err != nil || status != string(want) {
			t.Fatalf("file %s status=%q err=%v, want %s", fileID, status, err, want)
		}
	}
	// 以一次本人静音收尾，确认无变化的步骤没有留下通知。
	if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.owner, chat.Conversation.ID, true); err != nil {
		t.Fatal(err)
	}
	feed.expect(t, feed.notice(f.owner.User.ID, realtime.KindConversationStateChanged, chat.Conversation.ID, loadConversationStateVersion(t, f.db, chat.Conversation.ID, f.owner.User.ID)))
}

// TestCustomerProfileConversationInvalidation 验证联系人名称、首选邮箱与渠道名称实际变化时只通知企业客服共享受众，相同值不推进。
func TestCustomerProfileConversationInvalidation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	var contact struct {
		ID    string              `bun:"contact_id"`
		Stage domain.ContactStage `bun:"stage"`
	}
	if err := f.db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cci.contact_id, c.stage").
		Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id").
		Join("JOIN contacts AS c ON c.id = cci.contact_id").
		Where("cc.conversation_id = ?", f.conversationID).
		Scan(ctx, &contact); err != nil {
		t.Fatal(err)
	}
	feed := startRealtimeFeed(t, f.owner.Organization.ID)
	updateContact := contactaction.NewUpdateContactAction(f.db)
	updateChannel := channelaction.NewUpdateMessageChannelAction(f.db)
	// renameChannel 只修改渠道名称。
	renameChannel := func(name string) error {
		_, err := updateChannel.ExecuteBasics(ctx, f.owner, f.channelID, channelaction.MessageChannelBasicsInput{
			Name: name, DefaultLocale: domain.CustomerLocaleChineseSimplified,
		})
		return err
	}
	for _, step := range []struct {
		name   string
		change func() error
		delta  int64
	}{
		{"联系人改名", func() error {
			_, err := updateContact.Execute(ctx, f.owner, contact.ID, contactaction.ContactInput{DisplayName: "访客新名", ChannelID: f.channelID, Stage: contact.Stage})
			return err
		}, 1},
		{"联系人名称未变", func() error {
			_, err := updateContact.Execute(ctx, f.owner, contact.ID, contactaction.ContactInput{DisplayName: "访客新名", ChannelID: f.channelID, Stage: contact.Stage, Notes: "只改备注"})
			return err
		}, 0},
		{"联系人再次改名", func() error {
			_, err := updateContact.Execute(ctx, f.owner, contact.ID, contactaction.ContactInput{DisplayName: "访客第二个名字", ChannelID: f.channelID, Stage: contact.Stage})
			return err
		}, 1},
		{"新增首选邮箱", func() error {
			_, err := updateContact.Execute(ctx, f.owner, contact.ID, contactaction.ContactInput{DisplayName: "访客第二个名字", ChannelID: f.channelID, Stage: contact.Stage,
				Methods: []contactaction.MethodInput{{Type: domain.ContactMethodTypeEmail, Value: "visitor.touch@example.com"}}})
			return err
		}, 1},
		{"首选邮箱未变", func() error {
			_, err := updateContact.Execute(ctx, f.owner, contact.ID, contactaction.ContactInput{DisplayName: "访客第二个名字", ChannelID: f.channelID, Stage: contact.Stage, Notes: "邮箱未变",
				Methods: []contactaction.MethodInput{{Type: domain.ContactMethodTypeEmail, Value: "visitor.touch@example.com"}}})
			return err
		}, 0},
		{"渠道改名", func() error { return renameChannel("客服未读测试新名") }, 1},
		{"渠道名称未变", func() error { return renameChannel("客服未读测试新名") }, 0},
	} {
		before := loadConversationVersion(t, f.db, f.conversationID)
		if err := step.change(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		after := loadConversationVersion(t, f.db, f.conversationID)
		if after != before+step.delta {
			t.Fatalf("%s: 会话版本 = %d，want %d", step.name, after, before+step.delta)
		}
		if step.delta > 0 {
			feed.expect(t, feed.customerInbox(f.conversationID, after))
		}
	}
	// 档案名称优先于渠道身份名称，渠道身份自带名称时联系人改名同样推进。
	if _, err := f.db.NewUpdate().Table("contact_channel_identities").Set("display_name = ?", "渠道身份名称").Where("contact_id = ?", contact.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	before := loadConversationVersion(t, f.db, f.conversationID)
	if _, err := updateContact.Execute(ctx, f.owner, contact.ID, contactaction.ContactInput{DisplayName: "覆盖渠道名的联系人名", ChannelID: f.channelID, Stage: contact.Stage,
		Methods: []contactaction.MethodInput{{Type: domain.ContactMethodTypeEmail, Value: "visitor.touch@example.com"}}}); err != nil {
		t.Fatal(err)
	}
	after := loadConversationVersion(t, f.db, f.conversationID)
	if after != before+1 {
		t.Fatalf("渠道身份有名称时联系人改名的会话版本 %d -> %d", before, after)
	}
	feed.expect(t, feed.customerInbox(f.conversationID, after))
	// 以一次渠道改名收尾，确认无变化的步骤没有留下通知。
	if err := renameChannel("客服未读测试收尾"); err != nil {
		t.Fatal(err)
	}
	feed.expect(t, feed.customerInbox(f.conversationID, loadConversationVersion(t, f.db, f.conversationID)))
}

// profileConversationBarrier 在资料写入锁定会话集合之后暂停该事务。
type profileConversationBarrier struct {
	entered, release chan struct{}
	once             sync.Once
}

// BeforeQuery 保留资料写入的上下文。
func (b *profileConversationBarrier) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery 在资料写入按会话 ID 顺序锁定会话并推进版本后暂停，让其他写入在等待会话锁时与之交叠。
func (b *profileConversationBarrier) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	if event.Err != nil || !strings.Contains(event.Query, "ORDER BY id FOR UPDATE") {
		return
	}
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
}

// TestProfileInvalidationLockOrder 验证成员改名持有资料与会话锁时，其他成员的群消息与转交给该成员按统一锁序等待，不产生死锁。
func TestProfileInvalidationLockOrder(t *testing.T) {
	t.Parallel()
	f := newProfileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 成员回复客户后成为会话参与者，再把会话转交给群主，由群主在并发中转交回来。
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "成员回复"}); err != nil {
		t.Fatal(err)
	}
	// 转交会追加系统事件，按实测记录一次转交对客户会话版本的推进次数。
	beforeTransfer := loadConversationVersion(t, f.db, f.conversationID)
	if _, err := servicesessionaction.NewTransferServiceSessionAction(f.db, newGroupAgentCoordinator(f.db), nil, newTestTasks(f.db)).Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.owner.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	transferDelta := loadConversationVersion(t, f.db, f.conversationID) - beforeTransfer
	// 第三名成员入群，群消息与转交分别由不同账号发起，互不在账号行上等待。
	sender := newChatLockUser(t, f.db, f.owner)
	if _, err := groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{sender.OrganizationIdentity.ID}}); err != nil {
		t.Fatal(err)
	}
	before := f.versions(t)
	barrier := &profileConversationBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	f.db.AddQueryHook(barrier)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })

	renamed := make(chan error, 1)
	go func() {
		_, err := useraction.NewUpdateProfileAction(f.db).Execute(ctx, f.member, useraction.ProfileInput{DisplayName: "并发改名", Email: f.member.Account.Email})
		renamed <- err
	}()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// 群消息先锁发送者账号行再锁会话，转交先共享锁定目标身份再锁会话，二者都等待改名事务提交。
	sent := make(chan error, 1)
	go func() {
		_, err := newGroupSendAction(f.db).Execute(ctx, sender, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "改名期间的消息"})
		sent <- err
	}()
	transferred := make(chan error, 1)
	go func() {
		_, err := servicesessionaction.NewTransferServiceSessionAction(f.db, newGroupAgentCoordinator(f.db), nil, newTestTasks(f.db)).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.OrganizationIdentity.ID})
		transferred <- err
	}()
	// 两个写入都进入锁等待后再放行改名事务。
	for {
		var blocked int
		if err := f.db.NewRaw(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
				AND wait_event_type = 'Lock' AND cardinality(pg_blocking_pids(pid)) > 0`).Scan(ctx, &blocked); err != nil {
			t.Fatal(err)
		}
		if blocked >= 2 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
	release.Do(func() { close(barrier.release) })
	for name, done := range map[string]<-chan error{"改名": renamed, "群消息": sent, "转交": transferred} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s失败: %v", name, err)
			}
		case <-ctx.Done():
			t.Fatalf("%s等待超时: %v", name, ctx.Err())
		}
	}
	after := f.versions(t)
	if after[f.groupID] != before[f.groupID]+2 || after[f.conversationID] != before[f.conversationID]+1+transferDelta || after[f.directID] != before[f.directID]+1 {
		t.Fatalf("并发写入后的会话版本 before=%v after=%v", before, after)
	}
}
