//go:build server

package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/token"
	"github.com/uptrace/bun"
)

// Preview 描述持有邀请链接的人可以看到的邀请信息，受邀邮箱只展示掩码。
type Preview struct {
	WorkspaceName string
	WorkspaceSlug string
	InviterName   string
	MaskedEmail   string
	Status        domain.InvitationStatus
}

// Workspace 描述接受邀请后加入的工作区。
type Workspace struct {
	ID     string
	Name   string
	Slug   string
	Status domain.OrganizationLifecycleStatus
}

// PreviewQuery 按邀请令牌读取邀请信息。
type PreviewQuery struct {
	db *bun.DB
}

// NewPreviewQuery 创建邀请预览查询。
func NewPreviewQuery(db *bun.DB) *PreviewQuery {
	return &PreviewQuery{db: db}
}

// Execute 返回令牌对应邀请的工作区名称、发起人和掩码后的受邀邮箱；令牌不存在时返回 ErrInvitationInvalid。
func (q *PreviewQuery) Execute(ctx context.Context, value string) (Preview, error) {
	var row struct {
		WorkspaceName string                  `bun:"workspace_name"`
		WorkspaceSlug string                  `bun:"workspace_slug"`
		InviterName   string                  `bun:"inviter_name"`
		InvitedEmail  string                  `bun:"invited_email"`
		Status        domain.InvitationStatus `bun:"status"`
	}
	err := q.db.NewSelect().TableExpr("organization_invitations AS inv").
		ColumnExpr("o.name AS workspace_name, o.slug AS workspace_slug, COALESCE(ioi.display_name, '') AS inviter_name, inv.invited_email").
		ColumnExpr("CASE WHEN inv.status = ? AND inv.expires_at <= now() THEN ? ELSE inv.status END AS status", domain.InvitationStatusPending, domain.InvitationStatusExpired).
		Join("JOIN organizations AS o ON o.id = inv.organization_id").
		Join("LEFT JOIN users AS iu ON iu.id = inv.invited_by_user_id AND iu.organization_id = inv.organization_id").
		Join("LEFT JOIN organization_identities AS ioi ON ioi.id = iu.identity_id AND ioi.organization_id = iu.organization_id").
		Where("inv.token_hash = ?", token.Hash(value)).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) || value == "" {
		return Preview{}, ErrInvitationInvalid
	}
	if err != nil {
		return Preview{}, fmt.Errorf("load invitation preview: %w", err)
	}
	return Preview{WorkspaceName: row.WorkspaceName, WorkspaceSlug: row.WorkspaceSlug, InviterName: row.InviterName, MaskedEmail: maskEmail(row.InvitedEmail), Status: row.Status}, nil
}

// PendingEmail 在调用方事务内以共享锁读取有效邀请令牌对应的受邀邮箱，供注册时确认邀请，并发的撤销或重新生成等待注册事务结束；令牌无效、已处理或已过期时返回 ErrInvitationInvalid。
func PendingEmail(ctx context.Context, db bun.IDB, value string) (string, error) {
	var email string
	err := db.NewSelect().Model((*servermodels.OrganizationInvitation)(nil)).
		Column("invited_email").
		Where("token_hash = ?", token.Hash(value)).
		Where("status = ?", domain.InvitationStatusPending).
		Where("expires_at > now()").
		For("SHARE").
		Scan(ctx, &email)
	if errors.Is(err, sql.ErrNoRows) || value == "" {
		return "", ErrInvitationInvalid
	}
	return email, err
}

// AcceptAction 由受邀账号接受邀请并加入工作区。
type AcceptAction struct {
	db *bun.DB
}

// NewAcceptAction 创建接受邀请操作。
func NewAcceptAction(db *bun.DB) *AcceptAction {
	return &AcceptAction{db: db}
}

// Execute 在单个事务中校验邀请有效、账号尚未加入且账号邮箱与受邀邮箱一致，创建成员身份、标记账号邮箱已验证并把邀请置为已接受。
func (a *AcceptAction) Execute(ctx context.Context, account *servermodels.AccountIdentity, value string) (Workspace, error) {
	if value == "" {
		return Workspace{}, ErrInvitationInvalid
	}
	var workspace Workspace
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		invitation := &servermodels.OrganizationInvitation{}
		err := tx.NewSelect().Model(invitation).
			Where("inv.token_hash = ?", token.Hash(value)).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationInvalid
		}
		if err != nil {
			return err
		}
		if invitation.Status != string(domain.InvitationStatusPending) || !invitation.ExpiresAt.After(time.Now()) {
			return ErrInvitationInvalid
		}
		// 锁定账号，成员判断与邮箱比对都基于事务内的当前值，避免与修改邮箱并发时用旧邮箱通过校验。
		var email string
		if err := tx.NewSelect().Model((*servermodels.Account)(nil)).
			Column("email").
			Where("id = ?", account.Account.ID).
			For("UPDATE").
			Scan(ctx, &email); err != nil {
			return err
		}
		// 已是成员时直接提示进入工作区，不再比对邮箱。
		member, err := tx.NewSelect().Model((*servermodels.User)(nil)).
			Where("organization_id = ?", invitation.OrganizationID).
			Where("account_id = ?", account.Account.ID).
			Exists(ctx)
		if err != nil {
			return err
		}
		if member {
			return ErrAlreadyMember
		}
		if !strings.EqualFold(invitation.InvitedEmail, email) {
			return ErrEmailMismatch
		}
		// 邀请指定的角色已删除时邀请失效，由发起方重新邀请。
		if _, err := roleaction.ValidateAssignment(ctx, tx, invitation.OrganizationID, invitation.RoleID); errors.Is(err, roleaction.ErrAssignmentInvalid) {
			return ErrInvitationInvalid
		} else if err != nil {
			return err
		}
		displayName := invitation.DisplayName
		if displayName == "" {
			displayName = account.Account.DisplayName
		}
		organizationIdentity := &servermodels.OrganizationIdentity{
			OrganizationID: invitation.OrganizationID,
			Type:           string(domain.OrganizationIdentityTypeUser),
			DisplayName:    displayName,
			WorkStatus:     string(domain.WorkStatusWorking),
		}
		if _, err := tx.NewInsert().Model(organizationIdentity).
			Column("organization_id", "type", "display_name", "handles_service_requests", "work_status").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		user := &servermodels.User{
			IdentityID:     organizationIdentity.ID,
			OrganizationID: invitation.OrganizationID,
			AccountID:      account.Account.ID,
			RoleID:         invitation.RoleID,
			Status:         string(domain.IdentityStatusActive),
		}
		if _, err := tx.NewInsert().Model(user).
			Column("identity_id", "organization_id", "account_id", "role_id", "status").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(invitation).
			Set("status = ?", domain.InvitationStatusAccepted).
			Set("accepted_account_id = ?", account.Account.ID).
			Set("accepted_user_id = ?", user.ID).
			Set("accepted_at = now()").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		// 收到邀请链接并用受邀邮箱加入，视为邮箱已验证。
		if _, err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("email_verified_at = now()").
			Set("updated_at = now()").
			Where("id = ?", account.Account.ID).
			Where("email_verified_at IS NULL").
			Exec(ctx); err != nil {
			return err
		}
		organization := &servermodels.Organization{}
		if err := tx.NewSelect().Model(organization).Column("id", "name", "slug", "lifecycle_status").Where("o.id = ?", invitation.OrganizationID).Scan(ctx); err != nil {
			return err
		}
		workspace = Workspace{ID: organization.ID, Name: organization.Name, Slug: organization.Slug, Status: domain.OrganizationLifecycleStatus(organization.LifecycleStatus)}
		return nil
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("accept invitation: %w", err)
	}
	return workspace, nil
}

// maskEmail 保留邮箱首字符和域名，本地部分其余字符替换为固定数量的星号，不暴露长度。
func maskEmail(email string) string {
	local, host, found := strings.Cut(email, "@")
	if !found || local == "" {
		return email
	}
	runes := []rune(local)
	return string(runes[0]) + "***@" + host
}
