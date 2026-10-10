//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	installationaction "github.com/runforyou-ai/luway/internal/actions/installation"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	"github.com/runforyou-ai/luway/internal/actions/ratelimit"
	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// authOps 持有首次安装、账号会话和工作区列表的 Action 和 Query。
type authOps struct {
	realtimePrefix     string
	deployment         *deploymentaction.DeploymentState
	installWorkspace   *installationaction.InstallWorkspaceAction
	login              *authaction.LoginAction
	register           *accountaction.RegisterAction
	logout             *authaction.LogoutAction
	changePassword     *accountaction.ChangePasswordAction
	setPushDevice      *authaction.SetPushDeviceAction
	listWorkspaces     *workspaceaction.ListAccountWorkspacesQuery
	canCreateWorkspace *workspaceaction.CanCreateWorkspaceQuery
	createWorkspace    *workspaceaction.CreateWorkspaceAction
	limiter            *ratelimit.Limiter
	// db 解析账号在各工作区的成员身份。
	db *bun.DB
	// loadInbox 读取各工作区的提醒数量。
	loadInbox *inboxaction.LoadInboxQuery
	// files 解析文件地址与当前成员资料。
	files *fileOps
}

// GetRealtimeConnection 返回有效成员的实时频道和账号连接身份。
func (o *authOps) GetRealtimeConnection(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.RealtimeConnection, error) {
	members, err := authaction.ListMemberships(ctx, o.db, account)
	if err != nil {
		return appservice.RealtimeConnection{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, err)
	}
	result := appservice.RealtimeConnection{UserID: "a_" + account.Account.ID, SessionID: account.Session.ID, Prefix: o.realtimePrefix, Path: "/nats", Members: make([]appservice.RealtimeMember, 0, len(members))}
	for _, member := range members {
		result.Members = append(result.Members, RealtimeMemberChannels(member.WorkspaceID, member.UserID))
	}
	return result, nil
}

// RealtimeMemberChannels 把成员频道名称投影为前端连接契约。
func RealtimeMemberChannels(workspaceID, memberID string) appservice.RealtimeMember {
	channels := realtime.MemberChannels(workspaceID, memberID)
	return appservice.RealtimeMember{WorkspaceID: workspaceID, UserID: memberID, Channel: channels.Channel, InboxChannel: channels.InboxChannel, TypingChannel: channels.TypingChannel, InboxTypingChannel: channels.InboxTypingChannel}
}

// newAuthOps 创建首次安装、账号会话和工作区入口的业务实现依赖，certificates 为首次安装的 HTTPS 部署地址签发证书，taskEnqueuer 投递首次安装后的后台任务。
func newAuthOps(db *bun.DB, deployment *deploymentaction.DeploymentState, certificates *certificateaction.Certificates, taskEnqueuer servertask.TxEnqueuer, files *fileOps, initializer workspaceaction.Initializer) *authOps {
	return &authOps{
		deployment:         deployment,
		installWorkspace:   installationaction.NewInstallWorkspaceAction(db, taskEnqueuer, deployment, certificates, initializer),
		login:              authaction.NewLoginAction(db),
		limiter:            ratelimit.NewLimiter(db),
		register:           accountaction.NewRegisterAction(db),
		logout:             authaction.NewLogoutAction(db),
		changePassword:     accountaction.NewChangePasswordAction(db),
		setPushDevice:      authaction.NewSetPushDeviceAction(db),
		listWorkspaces:     workspaceaction.NewListAccountWorkspacesQuery(db),
		canCreateWorkspace: workspaceaction.NewCanCreateWorkspaceQuery(db),
		createWorkspace:    workspaceaction.NewCreateWorkspaceAction(db, initializer),
		db:                 db,
		loadInbox:          inboxaction.NewLoadInboxQuery(db),
		files:              files,
	}
}

// authFromSession 把新签发的登录会话转换为应用契约。
func authFromSession(output authaction.SessionOutput) appservice.Auth {
	return appservice.Auth{Account: accountFromModel(*output.Account), Token: output.Token, ExpiresAt: output.ExpiresAt}
}

// InstallationStatus 从本进程的部署状态快照返回部署名称、首次安装状态、注册策略是否开放注册，以及产品品牌和接口版本。
func (o *authOps) InstallationStatus(ctx context.Context, meta appservice.RequestMeta) (appservice.InstallationStatus, error) {
	snapshot := o.deployment.Current()
	current := brand.Current()
	return appservice.InstallationStatus{
		DeploymentName: snapshot.Settings.Name, Installed: snapshot.Installed, RegistrationOpen: snapshot.RegistrationPolicy == domain.RegistrationPolicyOpen,
		Brand:      appservice.Brand{Names: current.Names, SDKName: current.SDKName, LinkScheme: current.Slug},
		APIVersion: appservice.APIVersion, MinClientAPIVersion: appservice.MinClientAPIVersion,
	}, nil
}

// InstallWorkspace 在平台尚无账号时创建平台管理员和第一个工作区，并返回工作区和登录会话。
func (o *authOps) InstallWorkspace(ctx context.Context, meta appservice.RequestMeta, input appservice.InstallWorkspaceInput) (appservice.InstallWorkspaceResult, error) {
	output, err := o.installWorkspace.Execute(ctx, installationaction.InstallWorkspaceInput{
		PublicURL:     input.PublicURL,
		WorkspaceName: input.WorkspaceName,
		DisplayName:   input.DisplayName,
		Email:         input.Email,
		Password:      input.Password,
		Locale:        input.Locale,
		TimeZone:      input.TimeZone,
	})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InstallWorkspaceResult{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, accountFieldKeys))
	}
	if issueErr, ok := certificateIssueError(ctx, meta, err); ok {
		return appservice.InstallWorkspaceResult{}, issueErr
	}
	if errors.Is(err, installationaction.ErrAlreadyInstalled) {
		slog.InfoContext(ctx, "平台已完成首次安装")
		return appservice.InstallWorkspaceResult{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAlreadyInitialized).WithStatus(http.StatusConflict)
	}
	if err != nil {
		return appservice.InstallWorkspaceResult{}, appservice.FailedError(meta, i18n.ErrorInstallationFailed, err)
	}
	slog.InfoContext(logscope.WithMember(ctx, output.Identity.Workspace.ID, output.Identity.Account.ID), "首次安装完成")
	workspace := output.Identity.Workspace
	return appservice.InstallWorkspaceResult{
		Auth:      authFromSession(output.Session),
		Workspace: appservice.Workspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug, Status: appservice.WorkspaceStatus(workspace.LifecycleStatus)},
	}, nil
}

// Login 校验账号密码并返回登录会话。
func (o *authOps) Login(ctx context.Context, meta appservice.RequestMeta, input appservice.LoginInput) (appservice.Auth, error) {
	if err := o.allowAuth(ctx, meta, ratelimit.LoginByIP.For(ratelimit.IPSubject(appservice.ClientIP(ctx))), ratelimit.LoginByEmail.For(strings.ToLower(strings.TrimSpace(input.Email)))); err != nil {
		return appservice.Auth{}, err
	}
	output, err := o.login.Execute(ctx, authaction.LoginInput{Email: input.Email, Password: input.Password})
	if errors.Is(err, authaction.ErrInvalidCredentials) {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorInvalidCredentials, nil)
	}
	if err != nil {
		return appservice.Auth{}, appservice.FailedError(meta, i18n.ErrorLoginFailed, err)
	}
	slog.InfoContext(logscope.WithAccount(ctx, output.Account.ID), "账号登录成功")
	return authFromSession(output), nil
}

// allowAuth 为登录与注册入口消耗限速额度，超出时返回本地化的限速错误。
func (o authOps) allowAuth(ctx context.Context, meta appservice.RequestMeta, checks ...ratelimit.Check) error {
	err := o.limiter.Allow(ctx, checks...)
	if limited, ok := errors.AsType[*ratelimit.LimitedError](err); ok {
		return appservice.RateLimitedError(meta, limited.RetryAfter)
	}
	if err != nil {
		return appservice.FailedError(meta, i18n.ErrorInternal, err)
	}
	return nil
}

// Register 在平台开放注册或持有效邀请时注册本地账号并返回登录会话。
func (o *authOps) Register(ctx context.Context, meta appservice.RequestMeta, input appservice.RegisterInput) (appservice.Auth, error) {
	if err := o.allowAuth(ctx, meta, ratelimit.RegisterByIP.For(ratelimit.IPSubject(appservice.ClientIP(ctx)))); err != nil {
		return appservice.Auth{}, err
	}
	output, err := o.register.Execute(ctx, accountaction.NewAccountInput{
		DisplayName: input.DisplayName,
		Email:       input.Email,
		Password:    input.Password,
		Locale:      input.Locale,
		TimeZone:    input.TimeZone,
	}, input.InvitationToken)
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, accountFieldKeys))
	}
	if errors.Is(err, accountaction.ErrRegistrationClosed) {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorRegistrationClosed, nil)
	}
	if errors.Is(err, invitationaction.ErrInvitationInvalid) {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorInvitationInvalid, nil)
	}
	if errors.Is(err, invitationaction.ErrEmailMismatch) {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorInvitationEmailMismatch, nil)
	}
	if errors.Is(err, accountaction.ErrInstallationRequired) {
		return appservice.Auth{}, appservice.SessionError(meta, appservice.SessionStateSetup, i18n.ErrorInstallationRequired)
	}
	if err != nil {
		return appservice.Auth{}, appservice.FailedError(meta, i18n.ErrorRegistrationFailed, err)
	}
	slog.InfoContext(logscope.WithAccount(ctx, output.Account.ID), "账号注册成功")
	return authFromSession(output), nil
}

// Logout 删除当前登录会话。
func (o *authOps) Logout(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) error {
	if err := o.logout.Execute(ctx, account); err != nil {
		return appservice.FailedError(meta, i18n.ErrorLogoutFailed, err)
	}
	slog.InfoContext(ctx, "账号退出登录")
	return nil
}

// LoadAccount 返回当前登录账号。
func (o *authOps) LoadAccount(_ context.Context, _ appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.Account, error) {
	return accountFromModel(account.Account), nil
}

// ChangePassword 核验当前账号的密码并保存新密码，其他登录会话随之失效。
func (o *authOps) ChangePassword(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ChangePasswordInput) error {
	err := o.changePassword.Execute(ctx, account, accountaction.ChangePasswordInput{
		CurrentPassword: input.CurrentPassword,
		NewPassword:     input.NewPassword,
	})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, accountFieldKeys))
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if err != nil {
		return appservice.FailedError(meta, i18n.ErrorPasswordUpdateFailed, err)
	}
	slog.InfoContext(ctx, "密码修改成功")
	return nil
}

// SetPushDevice 把本设备的离线推送目标记到当前登录会话。
func (o *authOps) SetPushDevice(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PushDeviceInput) error {
	err := o.setPushDevice.Execute(ctx, account, authaction.PushDeviceInput{
		App: input.App, Platform: input.Platform, DeviceID: input.DeviceID,
	})
	if errors.Is(err, authaction.ErrPushDeviceInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if errors.Is(err, authaction.ErrIdentityNotFound) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if err != nil {
		return appservice.FailedError(meta, i18n.ErrorPushDeviceSetFailed, err)
	}
	return nil
}

// ListWorkspaces 返回当前账号作为有效成员可进入的工作区，以及当前账号能否再创建工作区。
func (o *authOps) ListWorkspaces(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WorkspaceList, error) {
	failed := func(err error) (appservice.WorkspaceList, error) {
		return appservice.WorkspaceList{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, err)
	}
	workspaces, err := o.listWorkspaces.Execute(ctx, account)
	if err != nil {
		return failed(err)
	}
	canCreate, err := o.canCreateWorkspace.Execute(ctx, account)
	if err != nil {
		return failed(err)
	}
	items := arr.Map(workspaces, func(workspace workspaceaction.Workspace) appservice.Workspace {
		return appservice.Workspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug, Status: appservice.WorkspaceStatus(workspace.Status)}
	})
	return appservice.WorkspaceList{Items: items, CanCreate: canCreate}, nil
}

// CreateWorkspace 在平台创建策略和平台工作区上限允许时创建工作区，当前账号成为首位管理员成员。
func (o *authOps) CreateWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.WorkspaceInput) (appservice.Workspace, error) {
	workspace, err := o.createWorkspace.Execute(ctx, account, workspaceaction.WorkspaceInput{Name: input.Name})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.Workspace{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, workspaceFieldKeys))
	}
	if errors.Is(err, workspaceaction.ErrCreationNotAllowed) {
		return appservice.Workspace{}, appservice.ForbiddenError(meta, i18n.ErrorWorkspaceCreationNotAllowed)
	}
	if errors.Is(err, workspaceaction.ErrWorkspaceLimitReached) {
		return appservice.Workspace{}, appservice.ConflictError(meta, i18n.ErrorWorkspaceLimitReached, "workspace_limit_reached")
	}
	if err != nil {
		return appservice.Workspace{}, appservice.FailedError(meta, i18n.ErrorWorkspaceCreateFailed, err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, workspace.ID), "工作区已创建")
	return appservice.Workspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug, Status: appservice.WorkspaceStatus(workspace.Status)}, nil
}

// LoadIdentity 返回当前账号在请求目标工作区中的成员身份。
func (o *authOps) LoadIdentity(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.Identity, error) {
	output, err := o.files.identityFromModel(ctx, identity)
	if err != nil {
		return appservice.Identity{}, appservice.FailedError(meta, i18n.ErrorUserReadFailed, err)
	}
	return output, nil
}

// accountFieldKeys 把账号与首次安装的校验错误码映射为本地化文案键。
var accountFieldKeys = map[common.FieldCode]i18n.Key{
	accountaction.ValidationDisplayNameRequired:         i18n.FieldDisplayNameRequired,
	accountaction.ValidationDisplayNameInvalid:          i18n.FieldDisplayNameInvalid,
	accountaction.ValidationEmailInvalid:                i18n.FieldEmailInvalid,
	accountaction.ValidationEmailDuplicate:              i18n.FieldEmailDuplicate,
	accountaction.ValidationPasswordTooShort:            i18n.FieldPasswordTooShort,
	accountaction.ValidationPasswordTooLong:             i18n.FieldPasswordTooLong,
	accountaction.ValidationCurrentPasswordIncorrect:    i18n.FieldCurrentPasswordIncorrect,
	accountaction.ValidationLocaleInvalid:               i18n.FieldLocaleInvalid,
	accountaction.ValidationTimeZoneInvalid:             i18n.FieldTimeZoneInvalid,
	workspaceaction.ValidationNameRequired:              i18n.FieldWorkspaceNameRequired,
	workspaceaction.ValidationNameTooLong:               i18n.FieldWorkspaceNameTooLong,
	workspaceaction.ValidationNameDuplicate:             i18n.FieldWorkspaceNameDuplicate,
	certificateaction.ValidationPublicURLInvalid:        i18n.FieldPublicURLInvalid,
	certificateaction.ValidationPublicURLDomainRequired: i18n.FieldPublicURLDomainRequired,
}

// workspaceFieldKeys 把工作区名称的校验错误码映射为本地化文案键。
var workspaceFieldKeys = map[common.FieldCode]i18n.Key{
	workspaceaction.ValidationNameRequired:  i18n.FieldWorkspaceNameRequired,
	workspaceaction.ValidationNameTooLong:   i18n.FieldWorkspaceNameTooLong,
	workspaceaction.ValidationNameDuplicate: i18n.FieldWorkspaceNameDuplicate,
}

// ListWorkspaceAttention 逐个读取账号有效成员身份所在工作区的提醒数量；已暂停或读取期间失去成员身份的工作区不返回。
func (o *authOps) ListWorkspaceAttention(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WorkspaceAttentionList, error) {
	failed := func(err error) (appservice.WorkspaceAttentionList, error) {
		return appservice.WorkspaceAttentionList{}, appservice.FailedError(meta, i18n.ErrorInboxLoadFailed, err)
	}
	workspaces, err := o.listWorkspaces.Execute(ctx, account)
	if err != nil {
		return failed(err)
	}
	items := make([]appservice.WorkspaceAttention, 0, len(workspaces))
	for _, workspace := range workspaces {
		identity, err := authaction.ResolveMember(ctx, o.db, account, workspace.ID)
		if errors.Is(err, authaction.ErrMembershipNotFound) || errors.Is(err, authaction.ErrWorkspaceSuspended) {
			continue
		}
		if err != nil {
			return failed(err)
		}
		counts, err := o.loadInbox.LoadAttention(ctx, identity)
		if err != nil {
			return failed(err)
		}
		items = append(items, appservice.WorkspaceAttention{
			WorkspaceID: workspace.ID, AttentionUnreadCount: counts.Attention, PendingCount: counts.Pending, PendingUnreadCount: counts.PendingUnread,
		})
	}
	return appservice.WorkspaceAttentionList{Items: items}, nil
}
