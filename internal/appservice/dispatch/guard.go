//go:build server

package dispatch

import (
	"context"
	"errors"
	"log/slog"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	installationaction "github.com/runforyou-ai/luway/internal/actions/installation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Guard 校验登录会话并解析请求目标工作区中的成员身份，分发层在调用业务实现前经它认证与校验权限。
type Guard struct {
	installationStatus *installationaction.StatusQuery
	resolveAccount     *authaction.ResolveAccountQuery
	resolveAdmin       *authaction.ResolveAdminQuery
	resolveBearer      *authaction.ResolveBearerQuery
	resolveIdentity    *authaction.ResolveIdentityQuery
}

// NewGuard 创建会话守卫。
func NewGuard(db *bun.DB) Guard {
	return Guard{
		installationStatus: installationaction.NewStatusQuery(db),
		resolveAccount:     authaction.NewResolveAccountQuery(db),
		resolveAdmin:       authaction.NewResolveAdminQuery(db),
		resolveBearer:      authaction.NewResolveBearerQuery(db),
		resolveIdentity:    authaction.NewResolveIdentityQuery(db),
	}
}

// AuthenticateAccount 校验用户端 Bearer 会话，返回当前账号；支援会话不可用于账号级接口。
func (g Guard) AuthenticateAccount(ctx context.Context, meta appservice.RequestMeta) (context.Context, *servermodels.AccountIdentity, error) {
	account, err := g.resolveAccount.Execute(ctx, meta.Token)
	if errors.Is(err, authaction.ErrIdentityNotFound) {
		return ctx, nil, g.loginRequired(ctx, meta)
	}
	if err != nil {
		return ctx, nil, appservice.FailedError(meta, i18n.ErrorAuthenticationStatusFailed, err)
	}
	return logscope.WithAccount(ctx, account.Account.ID), account, nil
}

// AuthenticateBearer 校验用户端或支援 Bearer 会话，供实时连接等同时服务两种凭证的入口使用。
func (g Guard) AuthenticateBearer(ctx context.Context, meta appservice.RequestMeta) (context.Context, *servermodels.AccountIdentity, error) {
	account, err := g.resolveBearer.Execute(ctx, meta.Token)
	if errors.Is(err, authaction.ErrIdentityNotFound) {
		return ctx, nil, g.loginRequired(ctx, meta)
	}
	if err != nil {
		return ctx, nil, appservice.FailedError(meta, i18n.ErrorAuthenticationStatusFailed, err)
	}
	return logscope.WithAccount(ctx, account.Account.ID), account, nil
}

// AuthenticateAdmin 校验平台后台 Cookie 会话并确认当前账号是平台管理员。
func (g Guard) AuthenticateAdmin(ctx context.Context, meta appservice.RequestMeta) (context.Context, *servermodels.AccountIdentity, error) {
	account, err := g.resolveAdmin.Execute(ctx, meta.AdminToken)
	if errors.Is(err, authaction.ErrIdentityNotFound) {
		return ctx, nil, g.adminLoginRequired(ctx, meta)
	}
	if err != nil {
		return ctx, nil, appservice.FailedError(meta, i18n.ErrorAuthenticationStatusFailed, err)
	}
	if !account.Account.IsPlatformAdmin {
		slog.InfoContext(ctx, "非平台管理员调用平台管理接口")
		return ctx, nil, appservice.ForbiddenError(meta, i18n.ErrorPlatformAdminRequired)
	}
	return logscope.WithAccount(ctx, account.Account.ID), account, nil
}

// AuthenticateMember 校验用户端或支援 Bearer 会话，返回账号在请求目标工作区中的成员身份。
func (g Guard) AuthenticateMember(ctx context.Context, meta appservice.RequestMeta) (context.Context, *servermodels.Identity, error) {
	identity, err := g.resolveIdentity.Execute(ctx, meta.WorkspaceID, meta.Token)
	if errors.Is(err, authaction.ErrIdentityNotFound) || errors.Is(err, authaction.ErrSupportWorkspaceMismatch) {
		return ctx, nil, g.loginRequired(ctx, meta)
	}
	if errors.Is(err, authaction.ErrMembershipNotFound) {
		slog.InfoContext(ctx, "账号不是目标工作区的有效成员", "requested_workspace_id", meta.WorkspaceID)
		return ctx, nil, appservice.SessionError(meta, appservice.SessionStateWorkspace, i18n.ErrorWorkspaceUnavailable)
	}
	if errors.Is(err, authaction.ErrWorkspaceSuspended) {
		slog.InfoContext(logscope.WithWorkspace(ctx, meta.WorkspaceID), "目标工作区已暂停")
		return ctx, nil, appservice.SessionError(meta, appservice.SessionStateWorkspace, i18n.ErrorWorkspaceSuspended)
	}
	if err != nil {
		return ctx, nil, appservice.FailedError(meta, i18n.ErrorAuthenticationStatusFailed, err)
	}
	ctx = logscope.WithMember(ctx, identity.Workspace.ID, identity.Account.ID)
	if identity.Session.Kind == string(domain.SessionKindSupport) {
		slog.InfoContext(ctx, "平台支援会话访问工作区", "support_session_id", identity.Session.ID)
	}
	return ctx, identity, nil
}

// Authorize 校验成员所属角色授予指定权限，未授予时返回无权限错误。
func (g Guard) Authorize(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, permission domain.PermissionCode) error {
	if identity.HasPermission(permission) {
		return nil
	}
	slog.InfoContext(ctx, "成员角色未授予接口所需权限", "permission", permission, "role_kind", identity.Role.Kind)
	return appservice.ForbiddenError(meta, i18n.ErrorPermissionDenied)
}

func (g Guard) loginRequired(ctx context.Context, meta appservice.RequestMeta) error {
	installed, err := g.installationStatus.Execute(ctx)
	if err != nil {
		return appservice.FailedError(meta, i18n.ErrorInstallationStatusReadFailed, err)
	}
	if !installed {
		return appservice.SessionError(meta, appservice.SessionStateSetup, i18n.ErrorInstallationRequired)
	}
	return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
}

func (g Guard) adminLoginRequired(ctx context.Context, meta appservice.RequestMeta) error {
	installed, err := g.installationStatus.Execute(ctx)
	if err != nil {
		return appservice.FailedError(meta, i18n.ErrorInstallationStatusReadFailed, err)
	}
	if !installed {
		return appservice.SessionError(meta, appservice.SessionStateSetup, i18n.ErrorInstallationRequired)
	}
	return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
}
