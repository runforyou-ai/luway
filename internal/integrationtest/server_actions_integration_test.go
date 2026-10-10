//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/runforyou-ai/einorun"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	installationaction "github.com/runforyou-ai/luway/internal/actions/installation"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testAgentRuntime 把运行交给测试函数执行的运行时。
type testAgentRuntime struct {
	run func(context.Context, agentruntime.RunRequest, einorun.Feed) (agentruntime.RunResult, error)
}

// Run 调用测试函数执行运行。
func (r testAgentRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
	return r.run(ctx, request, feed)
}

// assertDirectAgentRunStatus 断言收件箱中指定 AI 聊天的运行状态与摘要。
func assertDirectAgentRunStatus(t *testing.T, conversations []inboxaction.ConversationSummary, conversationID string, status domain.AgentRunStatus, preview string) {
	t.Helper()
	for _, conversation := range conversations {
		if conversation.ID != conversationID {
			continue
		}
		require.NotNil(t, conversation.Agent, "agent inbox conversation = %#v", conversation)
		require.Equal(t, domain.ConversationTypeAgent, conversation.Type)
		require.NotNil(t, conversation.Agent.AgentRunStatus)
		require.Equal(t, status, *conversation.Agent.AgentRunStatus)
		require.NotNil(t, conversation.Agent.Preview)
		require.Equal(t, preview, *conversation.Agent.Preview)
		return
	}
	require.Failf(t, "agent inbox conversation not found", "%q", conversationID)
}

// assertInboxConversationPresence 校验指定会话是否出现在收件箱查询结果中。
func assertInboxConversationPresence(t *testing.T, conversations []inboxaction.ConversationSummary, conversationID string, want bool) {
	t.Helper()
	found := false
	for _, conversation := range conversations {
		if conversation.ID == conversationID {
			found = true
			break
		}
	}
	require.Equal(t, want, found, "inbox conversation %q presence: %#v", conversationID, conversations)
}

// serverActionsFixture 保存服务端核心操作测试的全局前置数据，以及按顺序执行的子测试之间共享的实体。
type serverActionsFixture struct {
	db              *bun.DB
	otherInstalled  servertest.InstalledWorkspace
	resolveIdentity *authaction.ResolveIdentityQuery
	loggedIn        *servertest.InstalledWorkspace
	login           *authaction.LoginAction
	profileEmail    string
	getChannel      *channelaction.GetWebsiteChannelQuery
	updateChannel   *channelaction.UpdateMessageChannelAction
	updateProfile   *useraction.UpdateProfileAction

	channel                *channelaction.MessageChannelRecord
	telegramChannel        *channelaction.MessageChannelRecord
	telegramConversationID string
	team                   *teamaction.TeamRecord
	memberRole             *servermodels.Role
	createdMember          *useraction.User
	resolvedAfterUpdate    *servermodels.Identity
}

// TestServerActionsWithPostgreSQL 验证服务端核心操作。
// 工作区创建与管理员登录是全局前置，留在顶层；其余按领域拆成有序子测试，
// 子测试之间存在数据依赖，必须按声明顺序执行，不可并行。
func TestServerActionsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	databaseConfig := servertest.DatabaseConfig(t)
	store, err := openSharedTestDatabase(context.Background(), databaseConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// 全局前置：创建工作区并校验初始状态，失败直接终止整个测试。
	db := store.DB()
	status := installationaction.NewStatusQuery(db)
	adminEmail := servertest.UniqueEmail("admin")
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name:        "演示测试公司",
		DisplayName: "管理员",
		Email:       adminEmail,
		Password:    "password123",
		Locale:      domain.LocaleEnglishUnitedStates,
		TimeZone:    "America/New_York",
	})
	require.NotEmpty(t, installed.Identity.User.RoleID)
	require.True(t, strings.HasPrefix(installed.Identity.Workspace.Name, "演示测试公司-"), "workspace name = %q", installed.Identity.Workspace.Name)
	require.Equal(t, "en-US", installed.Identity.Account.Locale)
	require.Equal(t, "America/New_York", installed.Identity.Account.TimeZone)
	require.True(t, installed.Identity.User.MessageNotificationsEnabled)
	require.Equal(t, string(domain.WorkStatusWorking), installed.Identity.WorkspaceIdentity.WorkStatus)
	require.NotEmpty(t, installed.Identity.Workspace.Slug)
	require.Equal(t, installed.Identity.Account.ID, installed.Identity.User.AccountID)
	require.NotEmpty(t, installed.Identity.User.IdentityID)
	require.NotEqual(t, installed.Identity.User.ID, installed.Identity.User.IdentityID)
	teamCount, err := db.NewSelect().Model((*servermodels.Team)(nil)).
		Where("workspace_id = ?", installed.Identity.Workspace.ID).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), teamCount, "team count after installation")
	teamMemberCount, err := db.NewSelect().Model((*servermodels.TeamMember)(nil)).
		Where("workspace_id = ?", installed.Identity.Workspace.ID).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), teamMemberCount, "team member count after installation")
	platformInstalled, err := status.Execute(context.Background())
	require.NoError(t, err)
	require.True(t, platformInstalled, "platform with accounts is not installed")
	otherInstalled := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name:        "另一家测试公司",
		DisplayName: "管理员",
		Email:       servertest.UniqueEmail("other-admin"),
		Password:    "password123",
	})
	require.NotEqual(t, installed.Identity.Workspace.ID, otherInstalled.Identity.Workspace.ID, "different workspaces resolved to the same workspace")
	// 管理员账号不是另一个工作区的成员，登录会话不能解析出该工作区的成员身份。
	resolveIdentity := authaction.NewResolveIdentityQuery(db)
	_, err = resolveIdentity.Execute(context.Background(), otherInstalled.Identity.Workspace.ID, installed.Token)
	require.ErrorIs(t, err, authaction.ErrMembershipNotFound, "cross workspace identity")
	// 非法工作区编号按无成员身份处理，无效令牌优先返回登录会话失效。
	_, err = resolveIdentity.Execute(context.Background(), "not-a-workspace", installed.Token)
	require.ErrorIs(t, err, authaction.ErrMembershipNotFound, "invalid workspace identity")
	_, err = resolveIdentity.Execute(context.Background(), "not-a-workspace", "invalid-token")
	require.ErrorIs(t, err, authaction.ErrIdentityNotFound, "invalid token identity")
	// 全局前置：解析安装令牌、登出并重新登录管理员，失败直接终止整个测试。
	identity, err := resolveIdentity.Execute(context.Background(), installed.Identity.Workspace.ID, installed.Token)
	require.NoError(t, err)
	require.NotNil(t, identity)
	require.Equal(t, adminEmail, identity.Account.Email)
	// 单次查询解析出的成员身份与安装时建立的身份一致。
	require.Equal(t, installed.Identity.Workspace.ID, identity.Workspace.ID)
	require.Equal(t, installed.Identity.Workspace.Slug, identity.Workspace.Slug)
	require.Equal(t, installed.Identity.User.ID, identity.User.ID)
	require.Equal(t, installed.Identity.WorkspaceIdentity.ID, identity.WorkspaceIdentity.ID)
	require.Equal(t, identity.Account.ID, identity.Session.AccountID)
	_, err = useraction.NewUpdateWorkStatusAction(db, testEnqueuer).Execute(context.Background(), identity, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusAway})
	require.NoError(t, err)

	logout := authaction.NewLogoutAction(db)
	require.NoError(t, logout.Execute(context.Background(), &servermodels.AccountIdentity{Account: identity.Account, Session: identity.Session}))
	_, err = resolveIdentity.Execute(context.Background(), installed.Identity.Workspace.ID, installed.Token)
	require.ErrorIs(t, err, authaction.ErrIdentityNotFound, "logged out identity")

	loggedIn := servertest.LoginMember(t, db, installed.Identity.Workspace.ID, strings.ToUpper(adminEmail[:1])+adminEmail[1:], "password123")
	require.Equal(t, installed.Identity.User.ID, loggedIn.Identity.User.ID, "login user")
	// 登录保留成员上次设置的工作状态。
	require.Equal(t, string(domain.WorkStatusAway), loggedIn.Identity.WorkspaceIdentity.WorkStatus, "login work status")

	// 跨子测试共享：前面子测试创建的实体和操作在后续子测试中继续使用。
	login := authaction.NewLoginAction(db)
	profileEmail := servertest.UniqueEmail("new")
	getChannel := channelaction.NewGetWebsiteChannelQuery(db)
	updateChannel := channelaction.NewUpdateMessageChannelAction(db)
	updateProfile := useraction.NewUpdateProfileAction(db)
	s := &serverActionsFixture{
		db: db, otherInstalled: otherInstalled, resolveIdentity: resolveIdentity, loggedIn: &loggedIn, login: login,
		profileEmail: profileEmail, getChannel: getChannel, updateChannel: updateChannel, updateProfile: updateProfile,
	}
	// runStep 在子测试失败时立即终止整个测试。
	runStep := func(name string, step func(t *testing.T)) {
		t.Helper()
		if !t.Run(name, step) {
			t.FailNow()
		}
	}

	runStep("消息渠道管理", s.testMessageChannels)
	runStep("团队与成员管理", s.testTeamsAndMembers)
	runStep("企业成员内部单聊", s.testDirectChat)
	runStep("企业成员基础群聊", s.testGroupChat)
	runStep("AI员工", s.testAgents)
	runStep("文件与个人资料", s.testFilesAndProfile)
	runStep("邮箱冲突与头像重试", s.testEmailConflictAndAvatarRetry)
	runStep("联系人管理", s.testContacts)
	runStep("联系人档案", s.testContactProfile)
}

// telegramBotAPIFake 记录机器人与 Webhook 调用的 Telegram 接口替身。
type telegramBotAPIFake struct {
	mu             sync.Mutex
	bot            telegramintegration.Bot
	getMeTokens    []string
	setWebhooks    []telegramintegration.Webhook
	deleteWebhooks []string
}

// GetMe 记录 Token 并返回测试机器人。
func (f *telegramBotAPIFake) GetMe(_ context.Context, token string) (telegramintegration.Bot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getMeTokens = append(f.getMeTokens, token)
	return f.bot, nil
}

// SetWebhook 记录最后一次注册参数。
func (f *telegramBotAPIFake) SetWebhook(_ context.Context, _ string, webhook telegramintegration.Webhook) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setWebhooks = append(f.setWebhooks, webhook)
	return nil
}

// DeleteWebhook 记录被清理的 Token。
func (f *telegramBotAPIFake) DeleteWebhook(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteWebhooks = append(f.deleteWebhooks, token)
	return nil
}

// webhooks 返回注册调用快照。
func (f *telegramBotAPIFake) webhooks() []telegramintegration.Webhook {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]telegramintegration.Webhook(nil), f.setWebhooks...)
}

// deletedTokens 返回删除调用快照。
func (f *telegramBotAPIFake) deletedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteWebhooks...)
}

var _ telegramintegration.BotAPI = (*telegramBotAPIFake)(nil)

// telegramProfilePhotoAPIStub 返回预设头像的 Telegram 接口替身。
type telegramProfilePhotoAPIStub struct {
	photo      *telegramintegration.ProfilePhoto
	downloaded telegramintegration.DownloadedPhoto
	err        error
}

// GetUserProfilePhoto 返回测试预设的当前头像。
func (s *telegramProfilePhotoAPIStub) GetUserProfilePhoto(context.Context, string, int64) (*telegramintegration.ProfilePhoto, error) {
	return s.photo, s.err
}

// DownloadPhoto 返回测试预设的头像内容。
func (s *telegramProfilePhotoAPIStub) DownloadPhoto(context.Context, string, string) (telegramintegration.DownloadedPhoto, error) {
	return s.downloaded, s.err
}

var _ telegramintegration.ProfilePhotoAPI = (*telegramProfilePhotoAPIStub)(nil)

// importedFileWriterStub 记录保存次数的导入文件写入替身。
type importedFileWriterStub struct {
	saved int
}

// Save 接受测试导入内容并返回固定 ETag。
func (s *importedFileWriterStub) Save(context.Context, *servermodels.File, []byte) (string, error) {
	s.saved++
	return "telegram-avatar-etag", nil
}

var _ fileaction.ContentWriter = (*importedFileWriterStub)(nil)

// testTrigger 是测试运行时中可认领的输入序号。
type testTrigger struct {
	Seq int64
}

// pendingTriggers 返回输入中大于 after 的可认领最新序号，没有可认领输入时为空。
func pendingTriggers(ctx context.Context, feed einorun.Feed, after int64) ([]testTrigger, error) {
	latest, err := feed.Pending(ctx, after)
	if err != nil || latest == 0 {
		return nil, err
	}
	return []testTrigger{{Seq: latest}}, nil
}

// runBlocks 把业务侧展示的内容块转换为运行时的内容块，供测试运行时交回结果。
func runBlocks(blocks []agentcontract.Block) []einorun.Block {
	converted := make([]einorun.Block, len(blocks))
	for i, block := range blocks {
		converted[i] = einorun.Block{ID: block.ID, Position: block.Position, ModelCallID: block.ModelCallID, Kind: einorun.BlockKind(block.Kind), Text: block.Payload.Text}
		if call := block.Payload.ToolCall; call != nil {
			converted[i].Call = new(runCall(*call))
		}
	}
	return converted
}

// runCall 把业务侧展示的工具调用转换为修订号为 1 的运行时调用记录：业务字段写为注解，提交确认或审批的调用带上提交载荷，等待外部结果的调用交由外部推进。
func runCall(call agentcontract.ToolCall) einorun.ToolCall {
	notes := map[string]string{agentcontract.NoteSource: string(call.Source)}
	if call.Source == "" {
		notes[agentcontract.NoteSource] = string(domain.AgentToolSourceBuiltin)
	}
	if call.Level != "" {
		notes[agentcontract.NoteLevel] = string(call.Level)
	}
	if call.MCPServer != "" {
		notes[agentcontract.NoteMCPServer] = call.MCPServer
	}
	if call.BusinessSystemID != "" {
		notes[agentcontract.NoteBusinessSystemID], notes[agentcontract.NoteBusinessSystemName] = call.BusinessSystemID, call.BusinessSystem
	}
	if len(call.BoundArguments) > 0 {
		bound, _ := json.Marshal(call.BoundArguments)
		notes[agentcontract.NoteBoundArguments] = string(bound)
	}
	if call.Evidence {
		notes[agentcontract.NoteEvidence] = "true"
	}
	converted := einorun.ToolCall{
		ID: call.ID, ParentID: call.ParentID, ModelCallID: call.ModelCallID, CallID: call.CallID, Name: call.Name, Arguments: call.Arguments,
		Rev: 1, Result: call.Result, Error: call.Error, Status: einorun.CallStatus(call.Status), StartedAt: call.StartedAt, CompletedAt: call.CompletedAt,
		Replayable: call.Replayable, SideEffects: call.SideEffects, Notes: notes,
	}
	if call.Intervention != "" {
		payload, _ := json.Marshal(agentcontract.Submission{Intervention: call.Intervention, Target: call.Target})
		converted.Handover, converted.Payload = einorun.HandoverSubmitted, payload
	}
	// 等待外部结果的调用由外部推进。
	if call.Status == domain.AgentToolCallWaiting {
		converted.Handover = einorun.HandoverAwait
	}
	return converted
}
