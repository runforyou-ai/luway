//go:build server

package channelbinding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrChannelNotFound 表示渠道不存在或不服务员工。
	ErrChannelNotFound = errors.New("employee channel not found")
	// ErrIdentityNotFound 表示渠道身份不存在。
	ErrIdentityNotFound = errors.New("channel identity not found")
	// ErrMemberNotFound 表示待绑定的成员不存在或已停用。
	ErrMemberNotFound = errors.New("binding member not found")
)

// Account 是服务员工渠道中的一个外部账号及其绑定成员。
type Account struct {
	ID          string     `bun:"id"`
	ExternalID  string     `bun:"external_id"`
	DisplayName *string    `bun:"display_name"`
	LastSeenAt  *time.Time `bun:"last_seen_at"`
	CreatedAt   time.Time  `bun:"created_at"`
	// MemberIdentityID、MemberName 与 MemberAvatarFileID 在已绑定成员时有值。
	MemberIdentityID   *string `bun:"member_identity_id"`
	MemberName         *string `bun:"member_name"`
	MemberAvatarFileID *string `bun:"member_avatar_file_id"`
}

// ListAccountsQuery 列出服务员工渠道中的外部账号。
type ListAccountsQuery struct {
	db *bun.DB
}

// NewListAccountsQuery 创建外部账号列表查询。
func NewListAccountsQuery(db *bun.DB) *ListAccountsQuery {
	return &ListAccountsQuery{db: db}
}

// Execute 按最近活跃时间倒序返回渠道中的外部账号与绑定成员。
func (q *ListAccountsQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) ([]Account, error) {
	if err := ensureEmployeeChannel(ctx, q.db, identity.Workspace.ID, channelID, false); err != nil {
		return nil, err
	}
	accounts := make([]Account, 0)
	err := q.db.NewSelect().TableExpr("channel_identities AS ci").
		ColumnExpr("ci.id, ci.external_id, ci.display_name, ci.last_seen_at, ci.created_at").
		ColumnExpr("oi.id AS member_identity_id, oi.display_name AS member_name, oi.avatar_file_id AS member_avatar_file_id").
		Join("LEFT JOIN workspace_identities AS oi ON oi.id = ci.user_identity_id AND oi.workspace_id = ci.workspace_id").
		Where("ci.workspace_id = ? AND ci.channel_id = ?", identity.Workspace.ID, channelID).
		OrderExpr("ci.last_seen_at DESC NULLS LAST, ci.created_at DESC").
		Scan(ctx, &accounts)
	if err != nil {
		return nil, fmt.Errorf("list channel accounts: %w", err)
	}
	return accounts, nil
}

// ManageAction 由管理员绑定或解绑服务员工渠道中的外部账号。
type ManageAction struct {
	db *bun.DB
}

// NewManageAction 创建外部账号绑定管理操作。
func NewManageAction(db *bun.DB) *ManageAction {
	return &ManageAction{db: db}
}

// Bind 把外部账号绑定或改绑到在职成员；改绑后该外部账号的新消息进入以新成员为发起人的会话。
func (a *ManageAction) Bind(ctx context.Context, identity *servermodels.Identity, channelID, channelIdentityID, memberIdentityID string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		workspaceID := identity.Workspace.ID
		if err := ensureEmployeeChannel(ctx, tx, workspaceID, channelID, true); err != nil {
			return err
		}
		if err := ensureChannelIdentity(ctx, tx, workspaceID, channelID, channelIdentityID); err != nil {
			return err
		}
		active, err := tx.NewSelect().TableExpr("users AS u").
			Where("u.workspace_id = ? AND u.identity_id = ? AND u.status = ?", workspaceID, memberIdentityID, domain.IdentityStatusActive).
			For("SHARE OF u").Exists(ctx)
		if err != nil {
			return err
		}
		if !active {
			return ErrMemberNotFound
		}
		return bind(ctx, tx, workspaceID, channelIdentityID, memberIdentityID, false)
	})
}

// Unbind 解除外部账号与成员的绑定，之后该外部账号再发消息时收到绑定链接。
func (a *ManageAction) Unbind(ctx context.Context, identity *servermodels.Identity, channelID, channelIdentityID string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		workspaceID := identity.Workspace.ID
		if err := ensureEmployeeChannel(ctx, tx, workspaceID, channelID, true); err != nil {
			return err
		}
		if err := ensureChannelIdentity(ctx, tx, workspaceID, channelID, channelIdentityID); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Model((*servermodels.ChannelIdentity)(nil)).
			Set("user_identity_id = NULL").
			Where("workspace_id = ? AND id = ?", workspaceID, channelIdentityID).Exec(ctx)
		return err
	})
}

// ensureEmployeeChannel 校验渠道属于工作区且服务员工，lock 为真时对渠道取共享锁。
func ensureEmployeeChannel(ctx context.Context, db bun.IDB, workspaceID, channelID string, lock bool) error {
	var channelType domain.ChannelType
	query := db.NewSelect().TableExpr("channels AS c").Column("c.type").Where("c.workspace_id = ? AND c.id = ?", workspaceID, channelID)
	if lock {
		query = query.For("SHARE OF c")
	}
	err := query.Scan(ctx, &channelType)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && domain.ChannelCapabilitiesOf(channelType).Audience != domain.ServiceAudienceEmployee) {
		return ErrChannelNotFound
	}
	return err
}

// ensureChannelIdentity 校验渠道身份属于该渠道。
func ensureChannelIdentity(ctx context.Context, db bun.IDB, workspaceID, channelID, channelIdentityID string) error {
	exists, err := db.NewSelect().Model((*servermodels.ChannelIdentity)(nil)).
		Where("workspace_id = ? AND channel_id = ? AND id = ?", workspaceID, channelID, channelIdentityID).Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return ErrIdentityNotFound
	}
	return nil
}
