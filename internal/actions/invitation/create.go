//go:build server

package invitation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	commonemail "github.com/runforyou-ai/luway/pkg/email"
	"github.com/uptrace/bun"
)

// Input 定义发起邀请的字段；DisplayName 为空时接受邀请后使用账号名称。
type Input struct {
	Email       string
	DisplayName string
	RoleID      string
}

// CreateAction 由工作区成员发起邀请。
type CreateAction struct {
	db        *bun.DB
	mailer    Mailer
	publicURL string
}

// NewCreateAction 创建发起邀请操作，mailer 为空时只返回邀请链接。
func NewCreateAction(db *bun.DB, mailer Mailer, publicURL string) *CreateAction {
	return &CreateAction{db: db, mailer: mailer, publicURL: publicURL}
}

// Execute 校验受邀邮箱、显示名称和角色后创建邀请，同一邮箱的过期邀请先置为过期；配置了邮件发送时提交后发送邀请邮件。
func (a *CreateAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (Created, error) {
	input.Email = commonemail.Normalize(input.Email)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	fields := make(map[string]ValidationCode)
	if !commonemail.Valid(input.Email) {
		fields["email"] = ValidationEmailInvalid
	}
	if input.DisplayName != "" && !domain.IdentityDisplayNameValid(input.DisplayName) {
		fields["displayName"] = ValidationDisplayNameInvalid
	}
	if len(fields) > 0 {
		return Created{}, &ValidationError{Fields: fields}
	}
	var invitationID, value string
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := roleaction.ValidateAssignment(ctx, tx, identity.Organization.ID, input.RoleID); errors.Is(err, roleaction.ErrAssignmentInvalid) {
			return &ValidationError{Fields: map[string]ValidationCode{"roleId": ValidationRoleInvalid}}
		} else if err != nil {
			return err
		}
		member, err := tx.NewSelect().TableExpr("users AS u").
			Join("JOIN accounts AS acc ON acc.id = u.account_id").
			Where("u.organization_id = ?", identity.Organization.ID).
			Where("lower(acc.email) = lower(?)", input.Email).
			Exists(ctx)
		if err != nil {
			return err
		}
		if member {
			return &ValidationError{Fields: map[string]ValidationCode{"email": ValidationEmailMember}}
		}
		if err := expireStale(ctx, tx, identity.Organization.ID, input.Email); err != nil {
			return err
		}
		invitationID, value, err = insertInvitation(ctx, tx, identity, input.Email, input.DisplayName, input.RoleID)
		if pgerr.UniqueViolationOn(err, "organization_invitations_pending_email_unique") {
			return &ValidationError{Fields: map[string]ValidationCode{"email": ValidationEmailPending}}
		}
		return err
	})
	if err != nil {
		return Created{}, fmt.Errorf("create invitation: %w", err)
	}
	return a.created(ctx, identity, invitationID, value)
}

// created 读取新邀请，配置了邮件发送时在后台发送邀请邮件，并返回邀请链接。
func (a *CreateAction) created(ctx context.Context, identity *servermodels.Identity, invitationID, value string) (Created, error) {
	invitation, err := loadInvitation(ctx, a.db, identity.Organization.ID, invitationID)
	if err != nil {
		return Created{}, fmt.Errorf("load created invitation: %w", err)
	}
	link := Link(a.publicURL, value)
	if a.mailer != nil {
		invitation.EmailDelivery = true
		message := renderInvitationEmail(domain.Locale(identity.Account.Locale), identity.Organization.Name, identity.OrganizationIdentity.DisplayName, invitation.InvitedEmail, link)
		// 邀请链接已返回给发起人，邮件在后台发送，失败只记录日志。
		go func() {
			if err := a.mailer.Send(context.WithoutCancel(ctx), message); err != nil {
				slog.Warn("发送邀请邮件失败", "organization_id", identity.Organization.ID, "invitation_id", invitationID, "error", err)
			}
		}()
	}
	return Created{Invitation: invitation, Link: link}, nil
}

// expireStale 把同一邮箱已过期仍待接受的邀请置为过期，释放待接受邀请的唯一约束。
func expireStale(ctx context.Context, tx bun.Tx, organizationID, email string) error {
	_, err := tx.NewUpdate().Model((*servermodels.OrganizationInvitation)(nil)).
		Set("status = ?", domain.InvitationStatusExpired).
		Set("updated_at = now()").
		Where("organization_id = ?", organizationID).
		Where("lower(invited_email) = lower(?)", email).
		Where("status = ?", domain.InvitationStatusPending).
		Where("expires_at <= now()").
		Exec(ctx)
	return err
}
