//go:build server

package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/actions/seat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/runforyou-ai/support/str"
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
	Status domain.WorkspaceLifecycleStatus
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
	if value == "" {
		return Preview{}, ErrInvitationInvalid
	}
	var row struct {
		WorkspaceName string                  `bun:"workspace_name"`
		WorkspaceSlug string                  `bun:"workspace_slug"`
		InviterName   string                  `bun:"inviter_name"`
		InvitedEmail  string                  `bun:"invited_email"`
		Status        domain.InvitationStatus `bun:"status"`
	}
	err := q.db.NewSelect().TableExpr("workspace_invitations AS inv").
		ColumnExpr("o.name AS workspace_name, o.slug AS workspace_slug, COALESCE(ioi.display_name, '') AS inviter_name, inv.invited_email").
		ColumnExpr("CASE WHEN inv.status = ? AND inv.expires_at <= now() THEN ? ELSE inv.status END AS status", domain.InvitationStatusPending, domain.InvitationStatusExpired).
		Join("JOIN workspaces AS o ON o.id = inv.workspace_id").
		Join("LEFT JOIN users AS iu ON iu.id = inv.invited_by_user_id AND iu.workspace_id = inv.workspace_id").
		Join("LEFT JOIN workspace_identities AS ioi ON ioi.id = iu.identity_id AND ioi.workspace_id = iu.workspace_id").
		Where("? IN (inv.token_hash, inv.email_token_hash)", random.HashToken(value)).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return Preview{}, ErrInvitationInvalid
	}
	if err != nil {
		return Preview{}, fmt.Errorf("load invitation preview: %w", err)
	}
	return Preview{WorkspaceName: row.WorkspaceName, WorkspaceSlug: row.WorkspaceSlug, InviterName: row.InviterName, MaskedEmail: str.MaskEmail(row.InvitedEmail), Status: row.Status}, nil
}

// PendingEmail 在调用方事务内以共享锁读取有效邀请令牌对应的受邀邮箱，供注册时确认邀请，并发的撤销或重新生成等待注册事务结束；令牌无效、已处理或已过期时返回 ErrInvitationInvalid。
func PendingEmail(ctx context.Context, db bun.IDB, value string) (string, error) {
	if value == "" {
		return "", ErrInvitationInvalid
	}
	var email string
	err := db.NewSelect().Model((*servermodels.WorkspaceInvitation)(nil)).
		Column("invited_email").
		Where("? IN (inv.token_hash, inv.email_token_hash)", random.HashToken(value)).
		Where("status = ?", domain.InvitationStatusPending).
		Where("expires_at > now()").
		For("SHARE").
		Scan(ctx, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvitationInvalid
	}
	return email, err
}

// AcceptAction 由受邀账号接受邀请并加入工作区。
type AcceptAction struct {
	db    *bun.DB
	seats seat.Seats
}

// NewAcceptAction 创建接受邀请操作。
func NewAcceptAction(db *bun.DB, seats seat.Seats) *AcceptAction {
	return &AcceptAction{db: db, seats: seats}
}

// Execute 在单个事务中校验邀请有效、账号尚未加入、账号邮箱与受邀邮箱一致且未超出席位上限，创建成员身份、标记账号邮箱已验证并把邀请置为已接受。
func (a *AcceptAction) Execute(ctx context.Context, account *servermodels.AccountIdentity, value string) (Workspace, error) {
	if value == "" {
		return Workspace{}, ErrInvitationInvalid
	}
	var workspace Workspace
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		invitation := &servermodels.WorkspaceInvitation{}
		err := tx.NewSelect().Model(invitation).
			Where("? IN (inv.token_hash, inv.email_token_hash)", random.HashToken(value)).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationInvalid
		}
		if err != nil {
			return err
		}
		// 有效期判断取等待行锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if invitation.Status != string(domain.InvitationStatusPending) || !invitation.ExpiresAt.After(now) {
			return ErrInvitationInvalid
		}
		// 锁定账号，成员判断与邮箱比对都基于事务内账号邮箱的当前值。
		var email string
		if err := tx.NewSelect().Model((*servermodels.Account)(nil)).
			Column("email").
			Where("id = ?", account.Account.ID).
			For("UPDATE").
			Scan(ctx, &email); err != nil {
			return err
		}
		// 已是成员时直接提示进入工作区，跳过邮箱比对。
		member, err := tx.NewSelect().Model((*servermodels.User)(nil)).
			Where("workspace_id = ?", invitation.WorkspaceID).
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
		if _, err := roleaction.ValidateAssignment(ctx, tx, invitation.WorkspaceID, invitation.RoleID); errors.Is(err, roleaction.ErrAssignmentInvalid) {
			return ErrInvitationInvalid
		} else if err != nil {
			return err
		}
		displayName := invitation.DisplayName
		if displayName == "" {
			displayName = account.Account.DisplayName
		}
		workspaceIdentity := &servermodels.WorkspaceIdentity{
			WorkspaceID: invitation.WorkspaceID,
			Type:        string(domain.WorkspaceIdentityTypeUser),
			DisplayName: displayName,
			WorkStatus:  string(domain.WorkStatusWorking),
		}
		if _, err := tx.NewInsert().Model(workspaceIdentity).
			Column("workspace_id", "type", "display_name", "handles_service_requests", "work_status").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		user := &servermodels.User{
			IdentityID:  workspaceIdentity.ID,
			WorkspaceID: invitation.WorkspaceID,
			AccountID:   account.Account.ID,
			RoleID:      invitation.RoleID,
			Status:      string(domain.IdentityStatusActive),
		}
		if _, err := tx.NewInsert().Model(user).
			Column("identity_id", "workspace_id", "account_id", "role_id", "status").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		if err := a.seats.Enforce(ctx, tx, invitation.WorkspaceID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(invitation).
			Set("status = ?", domain.InvitationStatusAccepted).
			Set("accepted_account_id = ?", account.Account.ID).
			Set("accepted_user_id = ?", user.ID).
			Set("accepted_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		// 收到邀请链接并用受邀邮箱加入，视为邮箱已验证。
		if _, err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("email_verified_at = now()").
			Where("id = ?", account.Account.ID).
			Where("email_verified_at IS NULL").
			Exec(ctx); err != nil {
			return err
		}
		model := &servermodels.Workspace{}
		if err := tx.NewSelect().Model(model).Column("id", "name", "slug", "lifecycle_status").Where("o.id = ?", invitation.WorkspaceID).Scan(ctx); err != nil {
			return err
		}
		realtime.Notify(ctx, realtime.Notification{AudienceKind: realtime.AudienceAccount, AudienceID: account.Account.ID, Kind: realtime.KindMembershipAdded})
		workspace = Workspace{ID: model.ID, Name: model.Name, Slug: model.Slug, Status: domain.WorkspaceLifecycleStatus(model.LifecycleStatus)}
		return nil
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("accept invitation: %w", err)
	}
	return workspace, nil
}
