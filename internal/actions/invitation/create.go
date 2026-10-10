//go:build server

package invitation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/actions/seat"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
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
	db           *bun.DB
	enqueuer     servertask.TxEnqueuer
	seats        seat.Seats
	emailEnabled func() bool
	publicURL    func() string
}

// NewCreateAction 创建发起邀请操作；emailEnabled 返回部署当前是否配置了邮件发送，未配置时只返回邀请链接。
func NewCreateAction(db *bun.DB, enqueuer servertask.TxEnqueuer, seats seat.Seats, emailEnabled func() bool, publicURL func() string) *CreateAction {
	return &CreateAction{db: db, enqueuer: enqueuer, seats: seats, emailEnabled: emailEnabled, publicURL: publicURL}
}

// Execute 校验受邀邮箱、显示名称、角色与剩余席位，管理员角色只有管理员可以邀请，后创建邀请，同一邮箱的过期邀请先置为过期；配置了邮件发送时在同一事务内投递邀请邮件任务。
func (a *CreateAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (Created, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	fields := make(map[string]ValidationCode)
	if !str.IsEmail(input.Email) {
		fields["email"] = ValidationEmailInvalid
	}
	if input.DisplayName != "" && !domain.IdentityDisplayNameValid(input.DisplayName) {
		fields["displayName"] = ValidationDisplayNameInvalid
	}
	if len(fields) > 0 {
		return Created{}, &ValidationError{Fields: fields}
	}
	var invitationID, value string
	emailQueued := a.emailEnabled()
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := roleaction.ValidateAssignment(ctx, tx, identity.Workspace.ID, input.RoleID); errors.Is(err, roleaction.ErrAssignmentInvalid) {
			return &ValidationError{Fields: map[string]ValidationCode{"roleId": ValidationRoleInvalid}}
		} else if err != nil {
			return err
		}
		if err := roleaction.RequireAdministratorFor(ctx, tx, identity, nil, []string{input.RoleID}); err != nil {
			return err
		}
		member, err := tx.NewSelect().TableExpr("users AS u").
			Join("JOIN accounts AS acc ON acc.id = u.account_id").
			Where("u.workspace_id = ?", identity.Workspace.ID).
			Where("lower(acc.email) = lower(?)", input.Email).
			Exists(ctx)
		if err != nil {
			return err
		}
		if member {
			return &ValidationError{Fields: map[string]ValidationCode{"email": ValidationEmailMember}}
		}
		if err := a.seats.Check(ctx, tx, identity.Workspace.ID); err != nil {
			return err
		}
		if err := expireStale(ctx, tx, identity.Workspace.ID, input.Email); err != nil {
			return err
		}
		invitationID, value, err = insertInvitation(ctx, tx, identity, input.Email, input.DisplayName, input.RoleID)
		if pgerr.UniqueViolationOn(err, "workspace_invitations_pending_email_unique") {
			return &ValidationError{Fields: map[string]ValidationCode{"email": ValidationEmailPending}}
		}
		if err != nil || !emailQueued {
			return err
		}
		return a.enqueueEmail(ctx, tx, identity, invitationID)
	})
	if err != nil {
		return Created{}, fmt.Errorf("create invitation: %w", err)
	}
	return a.created(ctx, identity, invitationID, value, emailQueued)
}

// enqueueEmail 在调用方事务内投递按发起人界面语言发送的邀请邮件任务。
func (a *CreateAction) enqueueEmail(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, invitationID string) error {
	_, err := a.enqueuer.EnqueueIn(ctx, SendEmailActionName,
		SendEmailInput{WorkspaceID: identity.Workspace.ID, InvitationID: invitationID, Locale: domain.Locale(identity.Account.Locale)},
		servertask.EnqueueOptions{WorkspaceID: identity.Workspace.ID, MaxAttempts: sendEmailMaxAttempts, IdempotencyKey: "invitation-email:" + invitationID})
	return err
}

// created 读取新邀请并返回复制用的邀请链接，emailQueued 表示已投递邀请邮件任务。
func (a *CreateAction) created(ctx context.Context, identity *servermodels.Identity, invitationID, value string, emailQueued bool) (Created, error) {
	invitation, err := loadInvitation(ctx, a.db, identity.Workspace.ID, invitationID)
	if err != nil {
		return Created{}, fmt.Errorf("load created invitation: %w", err)
	}
	invitation.EmailDelivery = emailQueued
	return Created{Invitation: invitation, Link: Link(a.publicURL(), value)}, nil
}

// expireStale 把同一邮箱已过期仍待接受的邀请置为过期，释放待接受邀请的唯一约束。
func expireStale(ctx context.Context, tx bun.Tx, workspaceID, email string) error {
	_, err := tx.NewUpdate().Model((*servermodels.WorkspaceInvitation)(nil)).
		Set("status = ?", domain.InvitationStatusExpired).
		Where("workspace_id = ?", workspaceID).
		Where("lower(invited_email) = lower(?)", email).
		Where("status = ?", domain.InvitationStatusPending).
		Where("expires_at <= now()").
		Exec(ctx)
	return err
}
