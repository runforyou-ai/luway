//go:build server

// Package direct 在服务端进程内实现应用服务契约，解析登录身份后调用 Action 与 Query。
package direct

import (
	"context"
	"errors"
	"log/slog"
	"time"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	installationaction "github.com/runforyou-ai/luway/internal/actions/installation"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	knowledgebaseaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	mcpserveraction "github.com/runforyou-ai/luway/internal/actions/mcpserver"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/luway/internal/integration/commerce"
	"github.com/runforyou-ai/luway/internal/integration/control"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/productdocs"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

var (
	_ appservice.Backend            = (*Backend)(nil)
	_ appservice.WorkspaceInstaller = (*Backend)(nil)
)

// sessionGuard 校验登录会话并解析请求目标工作区中的成员身份。
type sessionGuard struct {
	db                 *bun.DB
	installationStatus *installationaction.StatusQuery
	resolveAccount     *authaction.ResolveAccountQuery
	resolveIdentity    *authaction.ResolveIdentityQuery
}

// Backend 解析登录身份并把业务调用分发给已认证实现。
//
// 各 Backend 方法的认证分发由 appservicegen 生成到 backend_gen.go；
// auth=public 的方法不解析身份，其余方法在调用实现前必须拿到当前身份。
type Backend struct {
	ops *directOperations
}

// directOperations 持有已认证业务实现所需的 Action 和 Query。
type directOperations struct {
	sessionGuard
	authOps
	conversationOps
	inboxOps
	channelOps
	contactOps
	directoryOps
	customerServiceOps
	aiPerformanceOps
	teamPerformanceOps
	serviceIssueOps
	knowledgeGapOps
	agentOps
	agentEvaluationOps
	personalAgentOps
	knowledgeOps
	integrationOps
	computerOps
	fileOps
	translationOps
	webSearchOps
	invitationOps
	platformOps
	platformModelOps
	creditOps
	commerceOps
	productDocsOps
}

// DeploymentConfig 定义直接后端的部署名称、部署地址、邀请邮件发送、产品文档、授权码验签公钥、control 客户端、上报开关缓存和商业服务客户端；邮件发送只在配置了 SMTP 时设置。
type DeploymentConfig struct {
	Name             string
	PublicURL        string
	InvitationMailer invitationaction.Mailer
	ProductDocs      *productdocs.Site
	LicenseKeys      license.Keys
	Control          *control.Client
	Commerce         *commerce.Client
	Telemetry        *platformaction.Telemetry
	// InstanceID 是本服务端进程编号，写入导出的诊断信息。
	InstanceID string
}

// New 创建直接访问服务端存储的应用后端。
func New(db *bun.DB, deployment DeploymentConfig, localFiles *serverfilecontent.LocalStore, s3 serverfilecontent.S3Config, agentScheduler conversationaction.AgentMessageScheduler, agentCoordinator *agentrunaction.ExecuteAction, taskEnqueuer servertask.TxEnqueuer, serviceReplySuggestions *agentrunaction.GenerateServiceReplySuggestionsAction, translator *translationaction.Translator, knowledgeRetrieval *knowledgebaseaction.RetrievalService) *Backend {
	connectionRunner := connectiontest.NewRunner(10 * time.Second)
	connectionClient := connectiontest.NewHTTPClient()
	modelProviderRegistry := modelprovider.NewRegistry(connectionClient)
	telegramAPI := telegram.NewClient(connectionClient)
	mcpTest := mcpserveraction.NewTestConnectionAction(mcpintegration.NewClient())
	mcpScheduler := mcpserveraction.NewToolsScheduler(taskEnqueuer)
	guard := sessionGuard{db: db, installationStatus: installationaction.NewStatusQuery(db), resolveAccount: authaction.NewResolveAccountQuery(db), resolveIdentity: authaction.NewResolveIdentityQuery(db)}
	documentQuery := knowledgebaseaction.NewDocumentQuery(db)
	ops := &directOperations{
		sessionGuard:       guard,
		authOps:            newAuthOps(db, deployment, taskEnqueuer),
		conversationOps:    newConversationOps(db, agentScheduler, agentCoordinator, taskEnqueuer),
		inboxOps:           newInboxOps(db, taskEnqueuer),
		channelOps:         newChannelOps(db, connectionRunner, telegramAPI),
		contactOps:         newContactOps(db),
		directoryOps:       newDirectoryOps(db, agentCoordinator, taskEnqueuer),
		customerServiceOps: newCustomerServiceOps(db),
		aiPerformanceOps:   newAIPerformanceOps(db),
		teamPerformanceOps: newTeamPerformanceOps(db),
		serviceIssueOps:    newServiceIssueOps(db),
		knowledgeGapOps:    newKnowledgeGapOps(db, taskEnqueuer),
		agentOps:           newAgentOps(db, agentCoordinator, serviceReplySuggestions),
		agentEvaluationOps: newAgentEvaluationOps(db, taskEnqueuer),
		personalAgentOps:   newPersonalAgentOps(db),
		knowledgeOps:       newKnowledgeOps(db, taskEnqueuer, documentQuery, knowledgeRetrieval),
		integrationOps:     newIntegrationOps(db, taskEnqueuer, connectionRunner, modelProviderRegistry, mcpTest, mcpScheduler),
		computerOps:        newComputerOps(db, taskEnqueuer),
		fileOps:            newFileOps(db, localFiles, s3, serverfilecontent.NewLinks("", s3.PublicBaseURL)),
		translationOps:     newTranslationOps(db, translator),
		webSearchOps:       newWebSearchOps(db, connectionRunner),
		invitationOps:      newInvitationOps(db, deployment.InvitationMailer, deployment.PublicURL),
		platformOps:        newPlatformOps(db, taskEnqueuer, deployment.LicenseKeys, deployment.Control, s3, deployment.Telemetry, deployment.InstanceID),
		platformModelOps:   newPlatformModelOps(db, modelProviderRegistry),
		creditOps:          newCreditOps(db),
		commerceOps:        newCommerceOps(db, deployment.Commerce, taskEnqueuer),
		productDocsOps:     productDocsOps{site: deployment.ProductDocs},
	}
	return &Backend{ops: ops}
}

// InstallWorkspace 完成首次安装并返回创建的工作区和平台管理员登录会话。
func (b *Backend) InstallWorkspace(ctx context.Context, meta appservice.RequestMeta, input appservice.InstallWorkspaceInput) (_ appservice.InstallWorkspaceResult, err error) {
	defer settle(ctx, "InstallWorkspace", &err, internalError(meta))
	return b.ops.InstallWorkspace(ctx, meta, input)
}

// AuthenticateMember 校验实时事件流请求携带的登录令牌并返回成员会话。
func (b *Backend) AuthenticateMember(ctx context.Context, meta appservice.RequestMeta) (_ MemberSession, err error) {
	defer settle(ctx, "AuthenticateMember", &err, internalError(meta))
	identity, err := b.ops.authenticate(ctx, meta)
	if err != nil {
		return MemberSession{}, err
	}
	return NewMemberSession(identity), nil
}

// AuthenticateAccountMembers 校验请求携带的账号登录令牌，返回账号会话及其全部有效成员身份，工作区动态事件流据此订阅各工作区的本人受众。
func (b *Backend) AuthenticateAccountMembers(ctx context.Context, meta appservice.RequestMeta) (_ AccountMembersSession, err error) {
	defer settle(ctx, "AuthenticateAccountMembers", &err, internalError(meta))
	account, err := b.ops.authenticateAccount(ctx, meta)
	if err != nil {
		return AccountMembersSession{}, err
	}
	memberships, err := authaction.ListMemberships(ctx, b.ops.db, account)
	if err != nil {
		return AccountMembersSession{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, err)
	}
	members := make([]WorkspaceMember, 0, len(memberships))
	for _, membership := range memberships {
		members = append(members, WorkspaceMember{OrganizationID: membership.OrganizationID, UserID: membership.UserID})
	}
	return AccountMembersSession{AccountID: account.Account.ID, SessionID: account.Session.ID, ExpiresAt: account.Session.ExpiresAt, Members: members}, nil
}

// MemberSyncHeads 返回实时事件流所属成员的同步探针值。
func (b *Backend) MemberSyncHeads(ctx context.Context, session MemberSession) (_ appservice.SyncHeads, err error) {
	defer settle(ctx, "MemberSyncHeads", &err, internalError(appservice.RequestMeta{}))
	return b.ops.GetSyncHeads(ctx, appservice.RequestMeta{}, session.identity)
}

// AuthorizeAgentRunStream 校验运行过程流请求方对运行所属会话的阅读资格，并返回运行所属会话编号。
func (b *Backend) AuthorizeAgentRunStream(ctx context.Context, meta appservice.RequestMeta, session MemberSession, runID string) (_ string, err error) {
	defer settle(ctx, "AuthorizeAgentRunStream", &err, internalError(meta))
	conversationID, err := b.ops.authorizeAgentRunStream.Execute(ctx, session.identity, runID)
	if err != nil {
		return "", agentRunProcessError(meta, err)
	}
	return conversationID, nil
}

// SubscribeAgentRunStream 订阅本进程中该运行当前执行尝试的过程流，返回订阅时的快照与取消订阅函数；
// 运行不在本进程执行时返回 false，调用方按持久事实收敛。
func (b *Backend) SubscribeAgentRunStream(runID string,
	onDelta func(runstream.Delta), onEnd func()) (runstream.Snapshot, func(), bool) {
	snapshot, subscription, running := b.ops.agentCoordinator.SubscribeRunStream(runID, onDelta, onEnd)
	if !running {
		return runstream.Snapshot{}, nil, false
	}
	return snapshot, subscription.Close, true
}

// authenticateAccount 校验登录会话并返回当前账号。
func (g sessionGuard) authenticateAccount(ctx context.Context, meta appservice.RequestMeta) (*servermodels.AccountIdentity, error) {
	account, err := g.resolveAccount.Execute(ctx, meta.Token)
	if errors.Is(err, authaction.ErrIdentityNotFound) {
		return nil, g.loginRequired(ctx, meta)
	}
	if err != nil {
		return nil, appservice.FailedError(meta, i18n.ErrorAuthenticationStatusFailed, err)
	}
	return account, nil
}

// authenticateAdmin 校验登录会话并确认当前账号是平台管理员。
func (g sessionGuard) authenticateAdmin(ctx context.Context, meta appservice.RequestMeta) (*servermodels.AccountIdentity, error) {
	account, err := g.authenticateAccount(ctx, meta)
	if err != nil {
		return nil, err
	}
	if !account.Account.IsPlatformAdmin {
		slog.Info("非平台管理员调用平台管理接口", "account_id", account.Account.ID)
		return nil, appservice.ForbiddenError(meta, i18n.ErrorPlatformAdminRequired)
	}
	return account, nil
}

// authenticate 校验登录会话并返回账号在请求目标工作区中的成员身份。
func (g sessionGuard) authenticate(ctx context.Context, meta appservice.RequestMeta) (*servermodels.Identity, error) {
	identity, err := g.resolveIdentity.Execute(ctx, meta.WorkspaceID, meta.Token)
	if errors.Is(err, authaction.ErrIdentityNotFound) {
		return nil, g.loginRequired(ctx, meta)
	}
	if errors.Is(err, authaction.ErrMembershipNotFound) {
		slog.Info("账号不是目标工作区的有效成员", "workspace_id", meta.WorkspaceID)
		return nil, appservice.SessionError(meta, appservice.SessionStateWorkspace, i18n.ErrorWorkspaceUnavailable)
	}
	if errors.Is(err, authaction.ErrWorkspaceSuspended) {
		slog.Info("目标工作区已暂停", "workspace_id", meta.WorkspaceID)
		return nil, appservice.SessionError(meta, appservice.SessionStateWorkspace, i18n.ErrorWorkspaceSuspended)
	}
	if err != nil {
		return nil, appservice.FailedError(meta, i18n.ErrorAuthenticationStatusFailed, err)
	}
	return identity, nil
}

// loginRequired 返回需要登录的会话错误；平台尚未完成首次安装时返回初始化入口。
func (g sessionGuard) loginRequired(ctx context.Context, meta appservice.RequestMeta) error {
	installed, err := g.installationStatus.Execute(ctx)
	if err != nil {
		return appservice.FailedError(meta, i18n.ErrorInstallationStatusReadFailed, err)
	}
	if !installed {
		return appservice.SessionError(meta, appservice.SessionStateSetup, i18n.ErrorInstallationRequired)
	}
	return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
}
