//go:build server

package direct

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

// identityFromModel 把存储身份转换为应用契约并补齐文件地址。
func (o *fileOps) identityFromModel(ctx context.Context, identity *servermodels.Identity) (appservice.Identity, error) {
	user, err := o.currentUserFromIdentity(ctx, identity)
	if err != nil {
		return appservice.Identity{}, err
	}
	return appservice.Identity{Workspace: workspaceFromModel(identity.Workspace), User: user}, nil
}

// workspaceFromModel 把存储工作区转换为应用契约。
func workspaceFromModel(workspace servermodels.Workspace) appservice.CurrentWorkspace {
	return appservice.CurrentWorkspace{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug}
}

// accountFromModel 把存储账号转换为应用契约。
func accountFromModel(account servermodels.Account) appservice.Account {
	return appservice.Account{
		ID: account.ID, Email: account.Email, DisplayName: account.DisplayName, Locale: appservice.Locale(account.Locale),
		TimeZone: account.TimeZone, IsPlatformAdmin: account.IsPlatformAdmin,
	}
}

// currentUserFromIdentity 把存储身份转换为当前成员契约，邮箱、语言和时区取自所属账号，并补齐头像地址。
func (o *fileOps) currentUserFromIdentity(ctx context.Context, identity *servermodels.Identity) (appservice.CurrentUser, error) {
	storedUser := identity.User
	workspaceIdentity := identity.WorkspaceIdentity
	user := appservice.CurrentUser{
		ID: storedUser.ID, IdentityID: storedUser.IdentityID, WorkspaceID: storedUser.WorkspaceID, Email: identity.Account.Email, DisplayName: workspaceIdentity.DisplayName,
		RoleID: storedUser.RoleID, Status: appservice.UserStatus(storedUser.Status), Locale: appservice.Locale(identity.Account.Locale), TimeZone: identity.Account.TimeZone,
		TranslationLanguage: support.Deref(storedUser.TranslationLanguage), MessageNotificationsEnabled: storedUser.MessageNotificationsEnabled,
		HandlesServiceRequests: workspaceIdentity.HandlesServiceRequests, WorkStatus: appservice.WorkStatus(workspaceIdentity.WorkStatus),
	}
	// 权限按角色实际拥有的权限输出。
	permissions := domain.EffectiveRolePermissions(identity.Role.Kind, identity.Role.Permissions)
	user.RoleKind = identity.Role.Kind
	user.Permissions = append(make([]appservice.PermissionCode, 0, len(permissions)), permissions...)
	fileID := identity.WorkspaceIdentity.AvatarFileID
	if fileID == nil || *fileID == "" {
		return user, nil
	}
	urls, err := o.activeFileURLs(ctx, identity, []string{*fileID})
	if err != nil {
		return appservice.CurrentUser{}, err
	}
	user.AvatarURL = urls[*fileID]
	return user, nil
}

// activeFileURLs 批量解析当前企业已关联文件的公开地址。
func (o *fileOps) activeFileURLs(ctx context.Context, identity *servermodels.Identity, fileIDs []string) (map[string]string, error) {
	locations, err := o.getFile.ListActiveLocations(ctx, identity, fileIDs)
	if err != nil {
		return nil, err
	}
	urls := make(map[string]string, len(locations))
	for _, location := range locations {
		contentURL, err := o.links.URL(location.StorageBackend, location.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("build public URL for file %s: %w", location.ID, err)
		}
		urls[location.ID] = contentURL
	}
	return urls, nil
}

// optionalFileURLs 批量解析可选文件编号的公开地址，跳过空编号。
func (o *fileOps) optionalFileURLs(ctx context.Context, identity *servermodels.Identity, fileIDs ...*string) (map[string]string, error) {
	ids := arr.FilterMap(fileIDs, func(fileID *string) (string, bool) {
		return support.Deref(fileID), support.Deref(fileID) != ""
	})
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	return o.activeFileURLs(ctx, identity, ids)
}

// optionalFileURL 返回可选文件编号对应的公开地址。
func optionalFileURL(urls map[string]string, fileID *string) string {
	if fileID == nil {
		return ""
	}
	return urls[*fileID]
}
