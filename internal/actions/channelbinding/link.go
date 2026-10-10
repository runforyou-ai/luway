//go:build server

// Package channelbinding 把服务员工渠道中的外部平台账号绑定到工作区成员：向未绑定的账号发送一次性绑定链接，成员登录后确认绑定，管理员也可以直接绑定或解绑。
package channelbinding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

var (
	// ErrLinkInvalid 表示绑定链接不存在、已过期或已使用。
	ErrLinkInvalid = errors.New("channel binding link invalid")
	// ErrNotMember 表示当前账号不是绑定链接所属工作区的在职成员。
	ErrNotMember = errors.New("account is not an active member of the binding workspace")
	// ErrIdentityBound 表示渠道身份已绑定成员。
	ErrIdentityBound = errors.New("channel identity already bound")
	// ErrMemberBound 表示成员在该渠道已绑定其他外部账号。
	ErrMemberBound = errors.New("member already bound in channel")
)

// Link 返回部署地址下确认绑定的链接，令牌位于片段中，不进入服务端访问日志。
func Link(publicURL, value string) string {
	return strings.TrimRight(publicURL, "/") + domain.WebAppPath + "#/channel-bindings/" + value
}

// IssuedLink 是新签发的绑定令牌编号与令牌原文。
type IssuedLink struct {
	TokenID string
	Token   string
}

// IssueLink 在调用方事务中为服务员工渠道的外部账号取得未绑定的渠道身份并签发绑定令牌；渠道身份已绑定，或发送间隔内已有确认送达且未使用的链接时返回空。
func IssueLink(ctx context.Context, db bun.IDB, channel *servermodels.Channel, externalID string) (*IssuedLink, error) {
	if _, err := db.NewInsert().Model(&servermodels.ChannelIdentity{
		ID: uuid.NewV7().String(), WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, ExternalID: externalID,
	}).Column("id", "workspace_id", "channel_id", "external_id").On("CONFLICT (channel_id, external_id) DO NOTHING").Exec(ctx); err != nil {
		return nil, fmt.Errorf("create unbound channel identity: %w", err)
	}
	identity := &servermodels.ChannelIdentity{}
	if err := db.NewSelect().Model(identity).
		Where("ci.workspace_id = ? AND ci.channel_id = ? AND ci.external_id = ?", channel.WorkspaceID, channel.ID, externalID).
		For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("lock unbound channel identity: %w", err)
	}
	if identity.UserIdentityID != nil {
		return nil, nil
	}
	recent, err := db.NewSelect().Model((*servermodels.ChannelBindingToken)(nil)).
		Where("workspace_id = ? AND channel_identity_id = ?", channel.WorkspaceID, identity.ID).
		Where("used_at IS NULL AND delivered_at > now() - make_interval(secs => ?)", domain.ChannelBindingLinkInterval.Seconds()).
		Exists(ctx)
	if err != nil || recent {
		return nil, err
	}
	token, tokenHash := random.Token(32)
	record := &servermodels.ChannelBindingToken{
		ID: uuid.NewV7().String(), WorkspaceID: channel.WorkspaceID, ChannelIdentityID: identity.ID,
		TokenHash: tokenHash,
	}
	if _, err := db.NewInsert().Model(record).Column("id", "workspace_id", "channel_identity_id", "token_hash", "expires_at").
		Value("expires_at", "now() + make_interval(secs => ?)", domain.ChannelBindingValidity.Seconds()).Exec(ctx); err != nil {
		return nil, fmt.Errorf("create channel binding token: %w", err)
	}
	return &IssuedLink{TokenID: record.ID, Token: token}, nil
}

// MarkLinkDelivered 记录平台已确认收到绑定链接，发送间隔内 IssueLink 据此跳过签发。
func MarkLinkDelivered(ctx context.Context, db bun.IDB, workspaceID, tokenID string) error {
	_, err := db.NewUpdate().Model((*servermodels.ChannelBindingToken)(nil)).Set("delivered_at = now()").
		Where("workspace_id = ? AND id = ?", workspaceID, tokenID).Exec(ctx)
	return err
}

// RevokeLink 删除平台明确未收到的绑定令牌，下次入站重新签发。
func RevokeLink(ctx context.Context, db bun.IDB, workspaceID, tokenID string) error {
	_, err := db.NewDelete().Model((*servermodels.ChannelBindingToken)(nil)).
		Where("workspace_id = ? AND id = ? AND used_at IS NULL", workspaceID, tokenID).Exec(ctx)
	return err
}

// Preview 描述持有绑定链接的人可以看到的工作区、渠道与外部账号。
type Preview struct {
	WorkspaceName string
	WorkspaceSlug string
	ChannelName   string
	ChannelType   domain.ChannelType
	ExternalName  string
	Status        domain.ChannelBindingStatus
}

// PreviewQuery 按绑定令牌读取绑定信息。
type PreviewQuery struct {
	db *bun.DB
}

// NewPreviewQuery 创建绑定链接预览查询。
func NewPreviewQuery(db *bun.DB) *PreviewQuery {
	return &PreviewQuery{db: db}
}

// Execute 返回令牌对应的工作区、渠道、外部账号与链接状态；令牌不存在时返回 ErrLinkInvalid。
func (q *PreviewQuery) Execute(ctx context.Context, value string) (Preview, error) {
	var preview Preview
	err := q.db.NewSelect().TableExpr("channel_binding_tokens AS cbt").
		ColumnExpr("o.name AS workspace_name, o.slug AS workspace_slug, ch.name AS channel_name, ch.type AS channel_type").
		ColumnExpr("COALESCE(ci.display_name, ci.external_id) AS external_name").
		ColumnExpr("CASE WHEN cbt.used_at IS NOT NULL OR ci.user_identity_id IS NOT NULL THEN ? WHEN cbt.expires_at <= now() THEN ? ELSE ? END AS status",
			domain.ChannelBindingStatusUsed, domain.ChannelBindingStatusExpired, domain.ChannelBindingStatusPending).
		Join("JOIN channel_identities AS ci ON ci.id = cbt.channel_identity_id AND ci.workspace_id = cbt.workspace_id").
		Join("JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Join("JOIN workspaces AS o ON o.id = cbt.workspace_id").
		Where("cbt.token_hash = ?", random.HashToken(value)).
		Scan(ctx, &preview.WorkspaceName, &preview.WorkspaceSlug, &preview.ChannelName, &preview.ChannelType, &preview.ExternalName, &preview.Status)
	if errors.Is(err, sql.ErrNoRows) || value == "" {
		return Preview{}, ErrLinkInvalid
	}
	if err != nil {
		return Preview{}, fmt.Errorf("load channel binding preview: %w", err)
	}
	return preview, nil
}

// ConfirmAction 由绑定链接所属工作区的成员确认把外部账号绑定到自己。
type ConfirmAction struct {
	db *bun.DB
}

// NewConfirmAction 创建确认绑定操作。
func NewConfirmAction(db *bun.DB) *ConfirmAction {
	return &ConfirmAction{db: db}
}

// Execute 在单个事务中校验令牌有效、当前账号是该工作区的在职成员，把渠道身份绑定到该成员并标记令牌已使用，返回工作区标识。
func (a *ConfirmAction) Execute(ctx context.Context, account *servermodels.AccountIdentity, value string) (string, error) {
	if value == "" {
		return "", ErrLinkInvalid
	}
	var slug string
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		record := &servermodels.ChannelBindingToken{}
		err := tx.NewSelect().Model(record).Where("cbt.token_hash = ?", random.HashToken(value)).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLinkInvalid
		}
		if err != nil {
			return err
		}
		// 有效期判断取等待行锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if record.UsedAt != nil || !record.ExpiresAt.After(now) {
			return ErrLinkInvalid
		}
		var member struct {
			IdentityID string `bun:"identity_id"`
			Slug       string `bun:"slug"`
		}
		err = tx.NewSelect().TableExpr("users AS u").ColumnExpr("u.identity_id, o.slug").
			Join("JOIN workspaces AS o ON o.id = u.workspace_id").
			Where("u.workspace_id = ? AND u.account_id = ? AND u.status = ?", record.WorkspaceID, account.Account.ID, domain.IdentityStatusActive).
			For("SHARE OF u").Scan(ctx, &member)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return err
		}
		if err := bind(ctx, tx, record.WorkspaceID, record.ChannelIdentityID, member.IdentityID, true); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(record).Set("used_at = now()").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("mark channel binding token used: %w", err)
		}
		slug = member.Slug
		return nil
	})
	return slug, err
}

// bind 锁定渠道身份并绑定到成员；requireUnbound 为真时已绑定的渠道身份返回 ErrIdentityBound，成员在同一渠道已绑定其他外部账号时返回 ErrMemberBound。
func bind(ctx context.Context, tx bun.Tx, workspaceID, channelIdentityID, userIdentityID string, requireUnbound bool) error {
	identity := &servermodels.ChannelIdentity{}
	if err := tx.NewSelect().Model(identity).Where("ci.workspace_id = ? AND ci.id = ?", workspaceID, channelIdentityID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("lock channel identity for binding: %w", err)
	}
	if requireUnbound && identity.UserIdentityID != nil {
		return ErrIdentityBound
	}
	taken, err := tx.NewSelect().Model((*servermodels.ChannelIdentity)(nil)).
		Where("workspace_id = ? AND channel_id = ? AND user_identity_id = ? AND id <> ?", workspaceID, identity.ChannelID, userIdentityID, identity.ID).
		Exists(ctx)
	if err != nil {
		return err
	}
	if taken {
		return ErrMemberBound
	}
	_, err = tx.NewUpdate().Model(identity).Set("user_identity_id = ?", userIdentityID).WherePK().Exec(ctx)
	return err
}
