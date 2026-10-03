//go:build server

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/token"
	"github.com/uptrace/bun"
)

var (
	// ErrIdentityNotFound 表示会话令牌无效、已过期或对应账号已停用。
	ErrIdentityNotFound = errors.New("session not found or account inactive")
	// ErrMembershipNotFound 表示账号在目标工作区没有有效的成员身份。
	ErrMembershipNotFound = errors.New("account is not an active member of the workspace")
	// ErrWorkspaceSuspended 表示目标工作区已被部署管理员暂停。
	ErrWorkspaceSuspended = errors.New("workspace is suspended")
)

// ResolveAccountQuery 解析登录会话令牌对应的账号。
type ResolveAccountQuery struct {
	db *bun.DB
}

// NewResolveAccountQuery 创建登录账号查询。
func NewResolveAccountQuery(db *bun.DB) *ResolveAccountQuery {
	return &ResolveAccountQuery{db: db}
}

// Execute 返回有效会话令牌对应的账号和会话。
func (q *ResolveAccountQuery) Execute(ctx context.Context, value string) (*servermodels.AccountIdentity, error) {
	return resolveAccount(ctx, q.db, value)
}

// ResolveIdentityQuery 解析登录会话在目标工作区中的成员身份。
type ResolveIdentityQuery struct {
	db *bun.DB
}

// NewResolveIdentityQuery 创建工作区成员身份查询。
func NewResolveIdentityQuery(db *bun.DB) *ResolveIdentityQuery {
	return &ResolveIdentityQuery{db: db}
}

// Execute 用一次查询返回会话令牌对应账号在目标工作区中的有效成员身份。
func (q *ResolveIdentityQuery) Execute(ctx context.Context, organizationID string, value string) (*servermodels.Identity, error) {
	if value == "" {
		return nil, ErrIdentityNotFound
	}
	// 非法工作区编号按空值匹配，账号有效时得到无成员身份的结果。
	var workspaceID any
	if common.ValidUUID(organizationID) {
		workspaceID = organizationID
	}
	identity := &servermodels.Identity{}
	err := q.db.NewRaw(`
		SELECT `+accountSessionColumns+`, `+memberColumns+`
		FROM account_sessions AS acs
		JOIN accounts AS acc ON acc.id = acs.account_id
		LEFT JOIN (`+memberTables+`) ON u.account_id = acc.id AND u.organization_id = ?
		WHERE acs.token_hash = ?
		  AND acs.expires_at > now()
	`, domain.OrganizationIdentityTypeUser, workspaceID, token.Hash(value)).Scan(ctx,
		append(accountSessionTargets(&identity.Account, &identity.Session), memberTargets(identity)...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIdentityNotFound
	}
	if err != nil {
		return nil, err
	}
	if identity.Account.Status != string(domain.AccountStatusActive) {
		return nil, ErrIdentityNotFound
	}
	// 成员行缺失时各成员字段为空值。
	if identity.User.ID == "" || identity.User.Status != string(domain.IdentityStatusActive) {
		return nil, ErrMembershipNotFound
	}
	if identity.Organization.LifecycleStatus != string(domain.OrganizationLifecycleActive) {
		return nil, ErrWorkspaceSuspended
	}
	return identity, nil
}

// accountSessionColumns 是账号与登录会话的查询列，扫描目标由 accountSessionTargets 给出。
const accountSessionColumns = `
	acc.id::text, acc.email, acc.email_verified_at, acc.display_name, acc.locale, acc.time_zone, acc.status, acc.is_deployment_admin,
	acs.id::text, acs.account_id::text, acs.expires_at`

// memberTables 是成员用户、成员身份与工作区的关联，成员身份类型取第一个查询参数。
const memberTables = `
	users AS u
	JOIN organization_identities AS oi ON oi.id = u.identity_id AND oi.organization_id = u.organization_id AND oi.type = ?
	JOIN organizations AS o ON o.id = u.organization_id`

// memberColumns 是工作区、成员用户与成员身份的查询列，扫描目标由 memberTargets 给出。
const memberColumns = `
	o.id::text, o.name, o.slug, o.lifecycle_status,
	u.id::text, u.identity_id::text, u.organization_id::text, u.account_id::text, u.status,
	u.translation_language, u.message_notifications_enabled, u.role_id::text,
	oi.id::text, oi.organization_id::text, oi.type, oi.display_name, oi.avatar_file_id::text, oi.handles_service_requests, oi.work_status`

// accountSessionTargets 返回与 accountSessionColumns 顺序一致的扫描目标。
func accountSessionTargets(account *servermodels.Account, session *servermodels.AccountSession) []any {
	return []any{
		&account.ID, &account.Email, &account.EmailVerifiedAt, &account.DisplayName,
		&account.Locale, &account.TimeZone, &account.Status, &account.IsDeploymentAdmin,
		&session.ID, &session.AccountID, &session.ExpiresAt,
	}
}

// memberTargets 返回与 memberColumns 顺序一致的扫描目标。
func memberTargets(identity *servermodels.Identity) []any {
	return []any{
		&identity.Organization.ID, &identity.Organization.Name, &identity.Organization.Slug, &identity.Organization.LifecycleStatus,
		&identity.User.ID, &identity.User.IdentityID, &identity.User.OrganizationID, &identity.User.AccountID, &identity.User.Status,
		&identity.User.TranslationLanguage, &identity.User.MessageNotificationsEnabled, &identity.User.RoleID,
		&identity.OrganizationIdentity.ID, &identity.OrganizationIdentity.OrganizationID, &identity.OrganizationIdentity.Type,
		&identity.OrganizationIdentity.DisplayName, &identity.OrganizationIdentity.AvatarFileID,
		&identity.OrganizationIdentity.HandlesServiceRequests, &identity.OrganizationIdentity.WorkStatus,
	}
}

// resolveAccount 返回有效会话令牌对应的账号；令牌无效、已过期或账号停用时返回 ErrIdentityNotFound。
func resolveAccount(ctx context.Context, db bun.IDB, value string) (*servermodels.AccountIdentity, error) {
	if value == "" {
		return nil, ErrIdentityNotFound
	}
	identity := &servermodels.AccountIdentity{}
	err := db.NewRaw(`
		SELECT `+accountSessionColumns+`
		FROM account_sessions AS acs
		JOIN accounts AS acc ON acc.id = acs.account_id
		WHERE acs.token_hash = ?
		  AND acs.expires_at > now()
	`, token.Hash(value)).Scan(ctx, accountSessionTargets(&identity.Account, &identity.Session)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIdentityNotFound
	}
	if err != nil {
		return nil, err
	}
	if identity.Account.Status != string(domain.AccountStatusActive) {
		return nil, ErrIdentityNotFound
	}
	return identity, nil
}

// ResolveMember 返回账号在目标工作区中的有效成员身份；没有成员身份或成员已停用时返回 ErrMembershipNotFound，工作区已暂停时返回 ErrWorkspaceSuspended。
func ResolveMember(ctx context.Context, db bun.IDB, account *servermodels.AccountIdentity, organizationID string) (*servermodels.Identity, error) {
	if !common.ValidUUID(organizationID) {
		return nil, ErrMembershipNotFound
	}
	identity := &servermodels.Identity{Account: account.Account, Session: account.Session}
	err := db.NewRaw(`
		SELECT `+memberColumns+`
		FROM `+memberTables+`
		WHERE u.organization_id = ?
		  AND u.account_id = ?
	`, domain.OrganizationIdentityTypeUser, organizationID, account.Account.ID).Scan(ctx, memberTargets(identity)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMembershipNotFound
	}
	if err != nil {
		return nil, err
	}
	if identity.User.Status != string(domain.IdentityStatusActive) {
		return nil, ErrMembershipNotFound
	}
	if identity.Organization.LifecycleStatus != string(domain.OrganizationLifecycleActive) {
		return nil, ErrWorkspaceSuspended
	}
	return identity, nil
}

// Membership 是账号在一个工作区中的有效成员身份。
type Membership struct {
	OrganizationID string `bun:"organization_id"`
	UserID         string `bun:"user_id"`
}

// ListMemberships 返回账号在正常或已暂停工作区中的全部有效成员身份，按工作区编号排序；工作区动态事件流据此订阅，已暂停工作区的恢复通知经本人受众送达。
func ListMemberships(ctx context.Context, db bun.IDB, account *servermodels.AccountIdentity) ([]Membership, error) {
	var memberships []Membership
	if err := db.NewSelect().
		TableExpr("users AS u").
		ColumnExpr("u.organization_id::text AS organization_id, u.id::text AS user_id").
		Join("JOIN organizations AS o ON o.id = u.organization_id").
		Where("u.account_id = ?", account.Account.ID).
		Where("u.status = ?", domain.IdentityStatusActive).
		Where("o.lifecycle_status IN (?)", bun.In([]domain.OrganizationLifecycleStatus{domain.OrganizationLifecycleActive, domain.OrganizationLifecycleSuspended})).
		OrderExpr("u.organization_id ASC").
		Scan(ctx, &memberships); err != nil {
		return nil, fmt.Errorf("list account memberships: %w", err)
	}
	return memberships, nil
}
