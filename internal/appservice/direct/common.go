//go:build server

package direct

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// identityFromModel 把存储身份转换为应用契约并补齐文件地址。
func (o *directOperations) identityFromModel(ctx context.Context, identity *servermodels.Identity) (appservice.Identity, error) {
	user, err := o.currentUserFromIdentity(ctx, identity)
	if err != nil {
		return appservice.Identity{}, err
	}
	return appservice.Identity{Organization: organizationFromModel(identity.Organization), User: user}, nil
}

// organizationFromModel 把存储工作区转换为应用契约。
func organizationFromModel(organization servermodels.Organization) appservice.Organization {
	return appservice.Organization{ID: organization.ID, Name: organization.Name, Slug: organization.Slug}
}

// accountFromModel 把存储账号转换为应用契约。
func accountFromModel(account servermodels.Account) appservice.Account {
	return appservice.Account{
		ID: account.ID, Email: account.Email, DisplayName: account.DisplayName, Locale: appservice.Locale(account.Locale),
		TimeZone: account.TimeZone, IsPlatformAdmin: account.IsPlatformAdmin,
	}
}

// currentUserFromIdentity 把存储身份转换为当前成员契约，邮箱、语言和时区取自所属账号，并补齐头像地址。
func (o *directOperations) currentUserFromIdentity(ctx context.Context, identity *servermodels.Identity) (appservice.CurrentUser, error) {
	storedUser := identity.User
	organizationIdentity := identity.OrganizationIdentity
	user := appservice.CurrentUser{
		ID: storedUser.ID, IdentityID: storedUser.IdentityID, OrganizationID: storedUser.OrganizationID, Email: identity.Account.Email, DisplayName: organizationIdentity.DisplayName,
		RoleID: storedUser.RoleID, Status: appservice.UserStatus(storedUser.Status), Locale: appservice.Locale(identity.Account.Locale), TimeZone: identity.Account.TimeZone,
		TranslationLanguage: common.StringValue(storedUser.TranslationLanguage), MessageNotificationsEnabled: storedUser.MessageNotificationsEnabled,
		HandlesServiceRequests: organizationIdentity.HandlesServiceRequests, WorkStatus: appservice.WorkStatus(organizationIdentity.WorkStatus),
	}
	fileID := identity.OrganizationIdentity.AvatarFileID
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
func (o *directOperations) activeFileURLs(ctx context.Context, identity *servermodels.Identity, fileIDs []string) (map[string]string, error) {
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
func (o *directOperations) optionalFileURLs(ctx context.Context, identity *servermodels.Identity, fileIDs ...*string) (map[string]string, error) {
	ids := make([]string, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		if fileID != nil && *fileID != "" {
			ids = append(ids, *fileID)
		}
	}
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

// commonActionError 转换各业务域共用的动作错误：请求已取消时原样返回，操作者身份失效时要求重新登录；其余错误返回 nil，由调用方继续按业务域映射。
func commonActionError(ctx context.Context, meta appservice.RequestMeta, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	return nil
}

// translateValidationFields 把校验错误码映射为本地化文案键。
func translateValidationFields[Code comparable](fields map[string]Code, keys map[Code]i18n.Key) map[string]i18n.Key {
	result := make(map[string]i18n.Key, len(fields))
	for field, code := range fields {
		key, exists := keys[code]
		if !exists {
			slog.Warn("未映射的校验错误码", "field", field, "code", fmt.Sprint(code))
			continue
		}
		result[field] = key
	}
	return result
}

// optionalDomain 把可选枚举指针转换为领域值，缺省为空。
func optionalDomain[T ~string, D ~string](value *T) D {
	if value == nil {
		return ""
	}
	return D(*value)
}
