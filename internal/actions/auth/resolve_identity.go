//go:build server

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

var (
	// ErrIdentityNotFound 表示会话令牌无效、已过期或对应账号已停用。
	ErrIdentityNotFound = errors.New("session not found or account inactive")
	// ErrMembershipNotFound 表示账号在目标工作区没有有效的成员身份。
	ErrMembershipNotFound = errors.New("account is not an active member of the workspace")
	// ErrWorkspaceSuspended 表示目标工作区已被平台管理员暂停。
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
func (q *ResolveIdentityQuery) Execute(ctx context.Context, workspaceID string, value string) (*servermodels.Identity, error) {
	if value == "" {
		return nil, ErrIdentityNotFound
	}
	// 非法工作区编号按空值匹配，账号有效时得到无成员身份的结果。
	var workspaceArg any
	if str.IsUUID(workspaceID) {
		workspaceArg = workspaceID
	}
	identity := &servermodels.Identity{}
	err := q.db.NewRaw(`
		SELECT `+accountSessionColumns+`, `+memberColumns+`
		FROM account_sessions AS acs
		JOIN accounts AS acc ON acc.id = acs.account_id
		LEFT JOIN (`+memberTables+`) ON u.account_id = acc.id AND u.workspace_id = ?
		WHERE acs.token_hash = ?
		  AND acs.expires_at > now()
	`, domain.WorkspaceIdentityTypeUser, workspaceArg, random.HashToken(value)).Scan(ctx,
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
	if identity.Workspace.LifecycleStatus != string(domain.WorkspaceLifecycleActive) {
		return nil, ErrWorkspaceSuspended
	}
	return identity, nil
}

// accountSessionColumns 是账号与登录会话的查询列，扫描目标由 accountSessionTargets 给出。
const accountSessionColumns = identityaction.AccountColumns + `,
	acs.id::text, acs.account_id::text, acs.expires_at, (extract(epoch FROM acs.expires_at - now()) * 1000000000)::bigint`

// memberTables 是成员用户、成员身份与工作区的关联，成员身份类型取第一个查询参数。
const memberTables = `
	users AS u
	JOIN workspace_identities AS oi ON oi.id = u.identity_id AND oi.workspace_id = u.workspace_id AND oi.type = ?
	JOIN workspaces AS o ON o.id = u.workspace_id
	JOIN roles AS r ON r.id = u.role_id AND r.workspace_id = u.workspace_id`

// memberColumns 是工作区、成员用户、成员身份与所属角色的查询列，扫描目标由 memberTargets 给出。
const memberColumns = `
	o.id::text, o.name, o.slug, o.lifecycle_status,` + identityaction.MemberColumns + `,
	r.kind, ARRAY(SELECT rp.permission FROM role_permissions AS rp WHERE rp.role_id = u.role_id)`

// accountSessionTargets 返回与 accountSessionColumns 顺序一致的扫描目标。
func accountSessionTargets(account *servermodels.Account, session *servermodels.AccountSession) []any {
	return append(identityaction.AccountTargets(account),
		&session.ID, &session.AccountID, &session.ExpiresAt, (*int64)(&session.Remaining))
}

// memberTargets 返回与 memberColumns 顺序一致的扫描目标。
func memberTargets(identity *servermodels.Identity) []any {
	targets := []any{&identity.Workspace.ID, &identity.Workspace.Name, &identity.Workspace.Slug, &identity.Workspace.LifecycleStatus}
	targets = append(targets, identityaction.MemberTargets(&identity.User, &identity.WorkspaceIdentity)...)
	return append(targets, &identity.Role.Kind, pgdialect.Array(&identity.Role.Permissions))
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
	`, random.HashToken(value)).Scan(ctx, accountSessionTargets(&identity.Account, &identity.Session)...)
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
func ResolveMember(ctx context.Context, db bun.IDB, account *servermodels.AccountIdentity, workspaceID string) (*servermodels.Identity, error) {
	if !str.IsUUID(workspaceID) {
		return nil, ErrMembershipNotFound
	}
	identity := &servermodels.Identity{Account: account.Account, Session: account.Session}
	err := db.NewRaw(`
		SELECT `+memberColumns+`
		FROM `+memberTables+`
		WHERE u.workspace_id = ?
		  AND u.account_id = ?
	`, domain.WorkspaceIdentityTypeUser, workspaceID, account.Account.ID).Scan(ctx, memberTargets(identity)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMembershipNotFound
	}
	if err != nil {
		return nil, err
	}
	if identity.User.Status != string(domain.IdentityStatusActive) {
		return nil, ErrMembershipNotFound
	}
	if identity.Workspace.LifecycleStatus != string(domain.WorkspaceLifecycleActive) {
		return nil, ErrWorkspaceSuspended
	}
	return identity, nil
}

// Membership 是账号在一个工作区中的有效成员身份。
type Membership struct {
	WorkspaceID string `bun:"workspace_id"`
	UserID      string `bun:"user_id"`
}

// ListMemberships 返回账号在正常工作区中的全部有效成员身份，按工作区编号排序。
func ListMemberships(ctx context.Context, db bun.IDB, account *servermodels.AccountIdentity) ([]Membership, error) {
	var memberships []Membership
	if err := db.NewSelect().
		TableExpr("users AS u").
		ColumnExpr("u.workspace_id::text AS workspace_id, u.id::text AS user_id").
		Join("JOIN workspaces AS o ON o.id = u.workspace_id").
		Where("u.account_id = ?", account.Account.ID).
		Where("u.status = ?", domain.IdentityStatusActive).
		Where("o.lifecycle_status = ?", domain.WorkspaceLifecycleActive).
		OrderExpr("u.workspace_id ASC").
		Scan(ctx, &memberships); err != nil {
		return nil, fmt.Errorf("list account memberships: %w", err)
	}
	return memberships, nil
}

// ResolveAccountSession 复核实时连接登记的账号与登录会话。
func ResolveAccountSession(ctx context.Context, db bun.IDB, accountID, sessionID string) (*servermodels.AccountIdentity, error) {
	if !str.IsUUID(accountID) || !str.IsUUID(sessionID) {
		return nil, ErrIdentityNotFound
	}
	identity := &servermodels.AccountIdentity{}
	err := db.NewRaw(`SELECT `+accountSessionColumns+` FROM account_sessions AS acs
 JOIN accounts AS acc ON acc.id = acs.account_id
 WHERE acs.id = ? AND acs.account_id = ? AND acs.expires_at > now()`, sessionID, accountID).
		Scan(ctx, accountSessionTargets(&identity.Account, &identity.Session)...)
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
