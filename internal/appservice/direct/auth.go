//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	installationaction "github.com/runforyou-ai/luway/internal/actions/installation"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	organizationaction "github.com/runforyou-ai/luway/internal/actions/organization"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// authOps 持有首次安装、账号会话和工作区列表的 Action 和 Query。
type authOps struct {
	deploymentName     string
	clientVersion      string
	platformSettings   *platformaction.SettingsQuery
	installWorkspace   *installationaction.InstallWorkspaceAction
	login              *authaction.LoginAction
	register           *accountaction.RegisterAction
	logout             *authaction.LogoutAction
	changePassword     *accountaction.ChangePasswordAction
	listWorkspaces     *organizationaction.ListAccountWorkspacesQuery
	canCreateWorkspace *organizationaction.CanCreateWorkspaceQuery
	createWorkspace    *organizationaction.CreateWorkspaceAction
}

// newAuthOps 创建首次安装、账号会话和工作区入口的业务实现依赖，taskEnqueuer 投递首次安装后的后台任务。
func newAuthOps(db *bun.DB, deployment DeploymentConfig, taskEnqueuer servertask.TxEnqueuer) authOps {
	return authOps{
		deploymentName:     deployment.Name,
		clientVersion:      deployment.ClientVersion,
		platformSettings:   platformaction.NewSettingsQuery(db),
		installWorkspace:   installationaction.NewInstallWorkspaceAction(db, taskEnqueuer),
		login:              authaction.NewLoginAction(db),
		register:           accountaction.NewRegisterAction(db),
		logout:             authaction.NewLogoutAction(db),
		changePassword:     accountaction.NewChangePasswordAction(db),
		listWorkspaces:     organizationaction.NewListAccountWorkspacesQuery(db),
		canCreateWorkspace: organizationaction.NewCanCreateWorkspaceQuery(db),
		createWorkspace:    organizationaction.NewCreateWorkspaceAction(db),
	}
}

// authFromSession 把新签发的登录会话转换为应用契约。
func authFromSession(output authaction.SessionOutput) appservice.Auth {
	return appservice.Auth{Account: accountFromModel(*output.Account), Token: output.Token, ExpiresAt: output.ExpiresAt}
}

// InstallationStatus 返回部署名称、首次安装状态、注册策略是否开放注册、产品品牌、接口版本和服务器提供的客户端版本。
func (o *directOperations) InstallationStatus(ctx context.Context, meta appservice.RequestMeta) (appservice.InstallationStatus, error) {
	settings, err := o.platformSettings.Execute(ctx)
	installed := !errors.Is(err, platformaction.ErrNotInstalled)
	if err != nil && installed {
		if ctx.Err() != nil {
			return appservice.InstallationStatus{}, ctx.Err()
		}
		slog.Warn("读取安装状态失败", "error", err)
		return appservice.InstallationStatus{}, appservice.FailedError(meta, i18n.ErrorInstallationStatusReadFailed)
	}
	current := brand.Current()
	return appservice.InstallationStatus{
		DeploymentName: o.deploymentName, Installed: installed, RegistrationOpen: settings.RegistrationPolicy == domain.RegistrationPolicyOpen,
		Brand:      appservice.Brand{Names: current.Names, SDKName: current.SDKName, LinkScheme: current.Slug},
		APIVersion: appservice.APIVersion, MinClientAPIVersion: appservice.MinClientAPIVersion, ClientVersion: o.clientVersion,
	}, nil
}

// InstallWorkspace 在平台尚无账号时创建平台管理员和第一个工作区，并返回登录会话。
func (o *directOperations) InstallWorkspace(ctx context.Context, meta appservice.RequestMeta, input appservice.InstallWorkspaceInput) (appservice.Auth, error) {
	output, err := o.installWorkspace.Execute(ctx, installationaction.InstallWorkspaceInput{
		WorkspaceName: input.WorkspaceName,
		WorkspaceSlug: input.WorkspaceSlug,
		DisplayName:   input.DisplayName,
		Email:         input.Email,
		Password:      input.Password,
		Locale:        domain.Locale(input.Locale),
		TimeZone:      input.TimeZone,
	})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, accountFieldKeys(validationError.Fields))
	}
	if errors.Is(err, installationaction.ErrAlreadyInstalled) {
		slog.Info("平台已完成首次安装")
		return appservice.Auth{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAlreadyInitialized).WithStatus(http.StatusConflict)
	}
	if err != nil {
		if ctx.Err() != nil {
			return appservice.Auth{}, ctx.Err()
		}
		slog.Warn("首次安装失败", "error", err)
		return appservice.Auth{}, appservice.FailedError(meta, i18n.ErrorInstallationFailed)
	}
	slog.Info("首次安装完成", "organization_id", output.Identity.Organization.ID, "account_id", output.Identity.Account.ID)
	return authFromSession(output.Session), nil
}

// Login 校验账号密码并返回登录会话。
func (o *directOperations) Login(ctx context.Context, meta appservice.RequestMeta, input appservice.LoginInput) (appservice.Auth, error) {
	output, err := o.login.Execute(ctx, authaction.LoginInput{Email: input.Email, Password: input.Password})
	if errors.Is(err, authaction.ErrInvalidCredentials) {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorInvalidCredentials, nil)
	}
	if err != nil {
		if ctx.Err() != nil {
			return appservice.Auth{}, ctx.Err()
		}
		slog.Warn("账号登录失败", "error", err)
		return appservice.Auth{}, appservice.FailedError(meta, i18n.ErrorLoginFailed)
	}
	slog.Info("账号登录成功", "account_id", output.Account.ID)
	return authFromSession(output), nil
}

// Register 在平台开放注册或持有效邀请时注册本地账号并返回登录会话。
func (o *directOperations) Register(ctx context.Context, meta appservice.RequestMeta, input appservice.RegisterInput) (appservice.Auth, error) {
	output, err := o.register.Execute(ctx, accountaction.NewAccountInput{
		DisplayName: input.DisplayName,
		Email:       input.Email,
		Password:    input.Password,
		Locale:      domain.Locale(input.Locale),
		TimeZone:    input.TimeZone,
	}, input.InvitationToken)
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.Auth{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, accountFieldKeys(validationError.Fields))
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
		if ctx.Err() != nil {
			return appservice.Auth{}, ctx.Err()
		}
		slog.Warn("注册账号失败", "error", err)
		return appservice.Auth{}, appservice.FailedError(meta, i18n.ErrorRegistrationFailed)
	}
	slog.Info("账号注册成功", "account_id", output.Account.ID)
	return authFromSession(output), nil
}

// Logout 删除当前登录会话。
func (o *directOperations) Logout(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) error {
	if err := o.logout.Execute(ctx, account); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("删除登录会话失败", "account_id", account.Account.ID, "error", err)
		return appservice.FailedError(meta, i18n.ErrorLogoutFailed)
	}
	slog.Info("账号退出登录", "account_id", account.Account.ID)
	return nil
}

// LoadAccount 返回当前登录账号。
func (o *directOperations) LoadAccount(_ context.Context, _ appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.Account, error) {
	return accountFromModel(account.Account), nil
}

// ChangePassword 核验当前账号的密码并保存新密码，其他登录会话随之失效。
func (o *directOperations) ChangePassword(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ChangePasswordInput) error {
	err := o.changePassword.Execute(ctx, account, accountaction.ChangePasswordInput{
		CurrentPassword: input.CurrentPassword,
		NewPassword:     input.NewPassword,
	})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, accountFieldKeys(validationError.Fields))
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("修改密码失败", "account_id", account.Account.ID, "error", err)
		return appservice.FailedError(meta, i18n.ErrorPasswordUpdateFailed)
	}
	slog.Info("密码修改成功", "account_id", account.Account.ID)
	return nil
}

// ListWorkspaces 返回当前账号作为有效成员可进入的工作区，以及当前账号能否再创建工作区。
func (o *directOperations) ListWorkspaces(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WorkspaceList, error) {
	failed := func(err error) (appservice.WorkspaceList, error) {
		if ctx.Err() != nil {
			return appservice.WorkspaceList{}, ctx.Err()
		}
		slog.Warn("读取工作区列表失败", "account_id", account.Account.ID, "error", err)
		return appservice.WorkspaceList{}, appservice.FailedError(meta, i18n.ErrorWorkspaceListFailed)
	}
	workspaces, err := o.listWorkspaces.Execute(ctx, account)
	if err != nil {
		return failed(err)
	}
	canCreate, err := o.canCreateWorkspace.Execute(ctx, account)
	if err != nil {
		return failed(err)
	}
	items := make([]appservice.Workspace, 0, len(workspaces))
	for _, workspace := range workspaces {
		items = append(items, appservice.Workspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug, Status: appservice.WorkspaceStatus(workspace.Status)})
	}
	return appservice.WorkspaceList{Items: items, CanCreate: canCreate}, nil
}

// CreateWorkspace 在平台创建策略和平台工作区上限允许时创建工作区，当前账号成为首位管理员成员。
func (o *directOperations) CreateWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.WorkspaceInput) (appservice.Workspace, error) {
	workspace, err := o.createWorkspace.Execute(ctx, account, organizationaction.WorkspaceInput{Name: input.Name, Slug: input.Slug})
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.Workspace{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, workspaceFieldKeys(validationError.Fields))
	}
	if errors.Is(err, organizationaction.ErrCreationNotAllowed) {
		return appservice.Workspace{}, appservice.ForbiddenError(meta, i18n.ErrorWorkspaceCreationNotAllowed)
	}
	if errors.Is(err, organizationaction.ErrWorkspaceLimitReached) {
		return appservice.Workspace{}, appservice.ConflictError(meta, i18n.ErrorWorkspaceLimitReached, "workspace_limit_reached")
	}
	if err != nil {
		if ctx.Err() != nil {
			return appservice.Workspace{}, ctx.Err()
		}
		slog.Warn("创建工作区失败", "account_id", account.Account.ID, "error", err)
		return appservice.Workspace{}, appservice.FailedError(meta, i18n.ErrorWorkspaceCreateFailed)
	}
	slog.Info("工作区已创建", "organization_id", workspace.ID, "account_id", account.Account.ID)
	return appservice.Workspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug, Status: appservice.WorkspaceStatus(workspace.Status)}, nil
}

// LoadIdentity 返回当前账号在请求目标工作区中的成员身份。
func (o *directOperations) LoadIdentity(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.Identity, error) {
	output, err := o.identityFromModel(ctx, identity)
	if err != nil {
		slog.Warn("读取当前成员头像失败", "organization_id", identity.Organization.ID, "user_id", identity.User.ID, "error", err)
		return appservice.Identity{}, appservice.FailedError(meta, i18n.ErrorUserReadFailed)
	}
	return output, nil
}

// accountFieldKeys 把账号与首次安装的校验错误码映射为本地化文案键。
func accountFieldKeys(fields map[string]common.FieldCode) map[string]i18n.Key {
	keys := map[common.FieldCode]i18n.Key{
		accountaction.ValidationDisplayNameRequired:      i18n.FieldDisplayNameRequired,
		accountaction.ValidationDisplayNameInvalid:       i18n.FieldDisplayNameInvalid,
		accountaction.ValidationEmailInvalid:             i18n.FieldEmailInvalid,
		accountaction.ValidationEmailDuplicate:           i18n.FieldEmailDuplicate,
		accountaction.ValidationPasswordTooShort:         i18n.FieldPasswordTooShort,
		accountaction.ValidationPasswordTooLong:          i18n.FieldPasswordTooLong,
		accountaction.ValidationCurrentPasswordIncorrect: i18n.FieldCurrentPasswordIncorrect,
		accountaction.ValidationLocaleInvalid:            i18n.FieldLocaleInvalid,
		accountaction.ValidationTimeZoneInvalid:          i18n.FieldTimeZoneInvalid,
		organizationaction.ValidationNameRequired:        i18n.FieldOrganizationNameRequired,
		organizationaction.ValidationNameTooLong:         i18n.FieldOrganizationNameTooLong,
		organizationaction.ValidationSlugInvalid:         i18n.FieldWorkspaceSlugInvalid,
		organizationaction.ValidationSlugTaken:           i18n.FieldWorkspaceSlugTaken,
	}
	return translateValidationFields(fields, keys)
}

// workspaceFieldKeys 把工作区名称和标识的校验错误码映射为本地化文案键。
func workspaceFieldKeys(fields map[string]common.FieldCode) map[string]i18n.Key {
	keys := map[common.FieldCode]i18n.Key{
		organizationaction.ValidationNameRequired: i18n.FieldOrganizationNameRequired,
		organizationaction.ValidationNameTooLong:  i18n.FieldOrganizationNameTooLong,
		organizationaction.ValidationSlugInvalid:  i18n.FieldWorkspaceSlugInvalid,
		organizationaction.ValidationSlugTaken:    i18n.FieldWorkspaceSlugTaken,
	}
	return translateValidationFields(fields, keys)
}

// ListWorkspaceAttention 逐个读取账号有效成员身份所在工作区的提醒数量；已暂停或读取期间失去成员身份的工作区不返回。
func (o *directOperations) ListWorkspaceAttention(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.WorkspaceAttentionList, error) {
	failed := func(err error) (appservice.WorkspaceAttentionList, error) {
		if ctx.Err() != nil {
			return appservice.WorkspaceAttentionList{}, ctx.Err()
		}
		slog.Warn("读取各工作区提醒数量失败", "account_id", account.Account.ID, "error", err)
		return appservice.WorkspaceAttentionList{}, appservice.FailedError(meta, i18n.ErrorInboxLoadFailed)
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
