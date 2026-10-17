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
	"github.com/runforyou-ai/luway/internal/actions/realtimeauth"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
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
	deployment         *deploymentaction.DeploymentState
	installPlatform    *installationaction.InstallPlatformAction
	login              *authaction.LoginAction
	adminLogin         *authaction.AdminLoginAction
	register           *accountaction.RegisterAction
	logout             *authaction.LogoutAction
	createSupport      *authaction.CreateSupportAccessAction
	revokeSupport      *authaction.RevokeSupportAccessAction
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

// GetRealtimeConnection 返回有效成员的实时频道和账号连接身份；支援会话只返回绑定工作区。
func (o *authOps) GetRealtimeConnection(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.RealtimeConnection, error) {
	var members []authaction.Membership
	var err error
	if account.Session.Kind == string(domain.SessionKindSupport) {
		if account.Session.WorkspaceID == nil {
			return appservice.RealtimeConnection{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, authaction.ErrSupportWorkspaceMismatch)
		}
		identity, resolveErr := authaction.ResolveMember(ctx, o.db, account, *account.Session.WorkspaceID)
		if resolveErr != nil {
			return appservice.RealtimeConnection{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, resolveErr)
		}
		members = []authaction.Membership{{WorkspaceID: identity.Workspace.ID, UserID: identity.User.ID}}
	} else {
		members, err = authaction.ListMemberships(ctx, o.db, account)
		if err != nil {
			return appservice.RealtimeConnection{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, err)
		}
	}
	result := appservice.RealtimeConnection{UserID: "a_" + account.Account.ID, SessionID: account.Session.ID, Path: "/realtime", Members: make([]appservice.RealtimeMember, 0, len(members))}
	for _, member := range members {
		result.Members = append(result.Members, RealtimeMemberChannels(member.WorkspaceID, member.UserID))
	}
	ticket, err := realtimeauth.IssueMember(ctx, o.db, account.Account.ID, account.Session.ID)
	if err != nil {
		return appservice.RealtimeConnection{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, err)
	}
	result.Token = ticket
	return result, nil
}

// RealtimeMemberChannels 把成员频道名称投影为前端连接契约。
func RealtimeMemberChannels(workspaceID, memberID string) appservice.RealtimeMember {
	channels := realtime.MemberChannels(workspaceID, memberID)
	return appservice.RealtimeMember{WorkspaceID: workspaceID, UserID: memberID, Channel: channels.Channel, InboxChannel: channels.InboxChannel, TypingChannel: channels.TypingChannel, InboxTypingChannel: channels.InboxTypingChannel}
}

// newAuthOps 创建首次安装、账号会话和工作区入口的业务实现依赖，certificates 为首次安装的 HTTPS 部署地址签发证书，taskEnqueuer 投递首次安装后的后台任务，platformInitializer 在首次安装事务内初始化附加数据，seats 在自助注册加入默认工作区时校验席位。
func newAuthOps(db *bun.DB, deployment *deploymentaction.DeploymentState, certificates *certificateaction.Certificates, taskEnqueuer servertask.TxEnqueuer, files *fileOps, initializer workspaceaction.Initializer, platformInitializer installationaction.PlatformInitializer, seats seataction.Seats) *authOps {
	return &authOps{
		deployment:         deployment,
		installPlatform:    installationaction.NewInstallPlatformAction(db, taskEnqueuer, deployment, certificates, initializer, platformInitializer),
		login:              authaction.NewLoginAction(db),
		adminLogin:         authaction.NewAdminLoginAction(db),
		limiter:            ratelimit.NewLimiter(db),
		register:           accountaction.NewRegisterAction(db, initializer, seats),
		logout:             authaction.NewLogoutAction(db),
		createSupport:      authaction.NewCreateSupportAccessAction(db),
		revokeSupport:      authaction.NewRevokeSupportAccessAction(db),
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

// adminAuthFromSession 把新签发的后台会话转换为应用契约。
func adminAuthFromSession(output authaction.SessionOutput) appservice.AdminAuth {
	return appservice.AdminAuth{Account: accountFromModel(*output.Account), Token: output.Token, ExpiresAt: output.ExpiresAt}
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

// InstallPlatform 在平台尚无账号时创建平台管理员并返回后台会话。
func (o *authOps) InstallPlatform(ctx context.Context, meta appservice.RequestMeta, input appservice.InstallPlatformInput) (appservice.AdminAuth, error) {
	output, err := o.installPlatform.Execute(ctx, installationaction.InstallPlatformInput{
		PublicURL:     input.PublicURL,
		WorkspaceName: input.WorkspaceName,
		DisplayName:   input.DisplayName,
		Email:         input.Email,
		Password:      input.Password,
		Locale:        input.Locale,
		TimeZone:      input.TimeZone,
	})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.AdminAuth{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, accountFieldKeys))
	}
	if issueErr, ok := certificateIssueError(ctx, meta, err); ok {
		return appservice.AdminAuth{}, issueErr
	}
	if errors.Is(err, installationaction.ErrAlreadyInstalled) {
		slog.InfoContext(ctx, "平台已完成首次安装")
		return appservice.AdminAuth{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAlreadyInitialized).WithStatus(http.StatusConflict)
	}
	if err != nil {
		return appservice.AdminAuth{}, appservice.FailedError(meta, i18n.ErrorInstallationFailed, err)
	}
	slog.InfoContext(logscope.WithAccount(ctx, output.Account.ID), "首次安装完成")
	return adminAuthFromSession(output.Session), nil
}

// Login 校验账号密码并返回用户端登录会话。
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

// AdminLogin 校验平台管理员密码并返回后台会话。
func (o *authOps) AdminLogin(ctx context.Context, meta appservice.RequestMeta, input appservice.LoginInput) (appservice.AdminAuth, error) {
	if err := o.allowAuth(ctx, meta, ratelimit.LoginByIP.For(ratelimit.IPSubject(appservice.ClientIP(ctx))), ratelimit.LoginByEmail.For(strings.ToLower(strings.TrimSpace(input.Email)))); err != nil {
		return appservice.AdminAuth{}, err
	}
	output, err := o.adminLogin.Execute(ctx, authaction.LoginInput{Email: input.Email, Password: input.Password})
	if errors.Is(err, authaction.ErrInvalidCredentials) {
		return appservice.AdminAuth{}, appservice.InvalidError(meta, i18n.ErrorInvalidCredentials, nil)
	}
	if err != nil {
		return appservice.AdminAuth{}, appservice.FailedError(meta, i18n.ErrorLoginFailed, err)
	}
	slog.InfoContext(logscope.WithAccount(ctx, output.Account.ID), "平台管理员登录成功")
	return adminAuthFromSession(output), nil
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
	if errors.Is(err, seataction.ErrLimitReached) {
		return appservice.Auth{}, appservice.ConflictError(meta, i18n.ErrorSeatLimitReached, "seat_limit_reached")
	}
	if errors.Is(err, workspaceaction.ErrDefaultWorkspaceMissing) {
		return appservice.Auth{}, appservice.ConflictError(meta, i18n.ErrorDefaultWorkspaceUnavailable, "default_workspace_unavailable")
	}
	if err != nil {
		return appservice.Auth{}, appservice.FailedError(meta, i18n.ErrorRegistrationFailed, err)
	}
	slog.InfoContext(logscope.WithAccount(ctx, output.Account.ID), "账号注册成功")
	return authFromSession(output), nil
}

// Logout 删除当前用户端登录会话。
func (o *authOps) Logout(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) error {
	if err := o.logout.Execute(ctx, account); err != nil {
		return appservice.FailedError(meta, i18n.ErrorLogoutFailed, err)
	}
	slog.InfoContext(ctx, "账号退出登录")
	return nil
}

// AdminLogout 删除当前平台后台会话。
func (o *authOps) AdminLogout(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) error {
	if err := o.logout.Execute(ctx, account); err != nil {
		return appservice.FailedError(meta, i18n.ErrorLogoutFailed, err)
	}
	slog.InfoContext(ctx, "平台管理员退出登录")
	return nil
}

// LoadAccount 返回当前用户端登录账号。
func (o *authOps) LoadAccount(_ context.Context, _ appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.Account, error) {
	return accountFromModel(account.Account), nil
}

// LoadAdminAccount 返回当前平台后台登录账号。
func (o *authOps) LoadAdminAccount(_ context.Context, _ appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.Account, error) {
	return accountFromModel(account.Account), nil
}

// ChangeAdminPassword 核验当前平台管理员的密码并保存新密码，其他登录会话随之失效。
func (o *authOps) ChangeAdminPassword(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ChangePasswordInput) error {
	return o.ChangePassword(ctx, meta, account, input)
}

// CreateSupportAccess 为平台管理员签发进入指定工作区的短期支援凭证。
func (o *authOps) CreateSupportAccess(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.SupportAccess, error) {
	output, err := o.createSupport.Execute(ctx, account, workspaceID)
	if errors.Is(err, authaction.ErrWorkspaceNotFound) {
		return appservice.SupportAccess{}, appservice.NotFoundError(meta, i18n.ErrorPlatformWorkspaceNotFound)
	}
	if errors.Is(err, authaction.ErrWorkspaceSuspended) {
		return appservice.SupportAccess{}, appservice.SessionError(meta, appservice.SessionStateWorkspace, i18n.ErrorWorkspaceSuspended)
	}
	if err != nil {
		return appservice.SupportAccess{}, appservice.FailedError(meta, i18n.ErrorInternal, err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, output.Workspace.ID), "平台支援会话已签发", "support_session_id", output.Session.SessionID)
	return appservice.SupportAccess{
		Token:     output.Session.Token,
		ExpiresAt: output.Session.ExpiresAt,
		Workspace: appservice.Workspace{
			ID: output.Workspace.ID, Name: output.Workspace.Name, Slug: output.Workspace.Slug,
			Status: appservice.WorkspaceStatus(output.Workspace.LifecycleStatus),
		},
	}, nil
}

// RevokeSupportAccess 撤销当前支援会话。
func (o *authOps) RevokeSupportAccess(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) error {
	err := o.revokeSupport.Execute(ctx, account)
	if errors.Is(err, authaction.ErrSessionKindMismatch) {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if err != nil {
		return appservice.FailedError(meta, i18n.ErrorLogoutFailed, err)
	}
	slog.InfoContext(ctx, "平台支援会话已撤销")
	return nil
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

// ListWorkspaces 返回当前账号作为有效成员可进入的工作区，以及当前账号能否再创建工作区；支援会话只返回绑定工作区且不可创建。
func (o *authOps) ListWorkspaces(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WorkspaceList, error) {
	failed := func(err error) (appservice.WorkspaceList, error) {
		return appservice.WorkspaceList{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed, err)
	}
	if account.Session.Kind == string(domain.SessionKindSupport) {
		workspace, err := o.supportWorkspace(ctx, account)
		if err != nil {
			return failed(err)
		}
		return appservice.WorkspaceList{
			Items: []appservice.Workspace{{
				ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug,
				Status: appservice.WorkspaceStatus(workspace.Status),
			}},
			CanCreate: false,
		}, nil
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

// ListWorkspaceAttention 逐个读取账号有效成员身份所在工作区的提醒数量；已暂停或读取期间失去成员身份的工作区不返回；支援会话只返回绑定工作区。
func (o *authOps) ListWorkspaceAttention(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WorkspaceAttentionList, error) {
	failed := func(err error) (appservice.WorkspaceAttentionList, error) {
		return appservice.WorkspaceAttentionList{}, appservice.FailedError(meta, i18n.ErrorInboxLoadFailed, err)
	}
	var workspaces []workspaceaction.Workspace
	if account.Session.Kind == string(domain.SessionKindSupport) {
		workspace, err := o.supportWorkspace(ctx, account)
		if err != nil {
			return failed(err)
		}
		workspaces = []workspaceaction.Workspace{workspace}
	} else {
		var err error
		workspaces, err = o.listWorkspaces.Execute(ctx, account)
		if err != nil {
			return failed(err)
		}
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

// supportWorkspace 返回支援会话绑定的有效成员工作区。
func (o *authOps) supportWorkspace(ctx context.Context, account *servermodels.AccountIdentity) (workspaceaction.Workspace, error) {
	if account.Session.WorkspaceID == nil {
		return workspaceaction.Workspace{}, authaction.ErrSupportWorkspaceMismatch
	}
	identity, err := authaction.ResolveMember(ctx, o.db, account, *account.Session.WorkspaceID)
	if err != nil {
		return workspaceaction.Workspace{}, err
	}
	return workspaceaction.Workspace{
		ID: identity.Workspace.ID, Name: identity.Workspace.Name, Slug: identity.Workspace.Slug,
		Status: domain.WorkspaceLifecycleStatus(identity.Workspace.LifecycleStatus),
	}, nil
}
