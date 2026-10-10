//go:build server

package identity

import servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"

// MemberColumns 是以 u、oi 为别名的成员用户与成员身份查询列，扫描目标由 MemberTargets 给出。
const MemberColumns = `
	u.id::text, u.identity_id::text, u.workspace_id::text, u.account_id::text, u.status,
	u.translation_language, u.message_notifications_enabled, u.role_id::text,
	oi.id::text, oi.workspace_id::text, oi.type, oi.display_name, oi.avatar_file_id::text, oi.handles_service_requests, oi.work_status`

// MemberTargets 返回与 MemberColumns 顺序一致的扫描目标。
func MemberTargets(user *servermodels.User, identity *servermodels.WorkspaceIdentity) []any {
	return []any{
		&user.ID, &user.IdentityID, &user.WorkspaceID, &user.AccountID, &user.Status,
		&user.TranslationLanguage, &user.MessageNotificationsEnabled, &user.RoleID,
		&identity.ID, &identity.WorkspaceID, &identity.Type,
		&identity.DisplayName, &identity.AvatarFileID,
		&identity.HandlesServiceRequests, &identity.WorkStatus,
	}
}

// AccountColumns 是以 acc 为别名的账号查询列，扫描目标由 AccountTargets 给出。
const AccountColumns = `
	acc.id::text, acc.email, acc.email_verified_at, acc.display_name, acc.locale, acc.time_zone, acc.status, acc.is_platform_admin`

// AccountTargets 返回与 AccountColumns 顺序一致的扫描目标。
func AccountTargets(account *servermodels.Account) []any {
	return []any{
		&account.ID, &account.Email, &account.EmailVerifiedAt, &account.DisplayName,
		&account.Locale, &account.TimeZone, &account.Status, &account.IsPlatformAdmin,
	}
}
