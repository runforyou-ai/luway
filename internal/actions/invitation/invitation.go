//go:build server

// Package invitation 实现工作区成员邀请的发起、撤销、预览和接受。
package invitation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/mail"
	"github.com/runforyou-ai/cervi/pkg/token"
	"github.com/uptrace/bun"
)

var (
	// ErrNotFound 表示当前工作区没有这条待接受的邀请。
	ErrNotFound = errors.New("invitation not found")
	// ErrInvitationInvalid 表示邀请令牌不存在，或邀请已接受、撤销或过期。
	ErrInvitationInvalid = errors.New("invitation is no longer valid")
	// ErrEmailMismatch 表示登录账号的邮箱与受邀邮箱不一致。
	ErrEmailMismatch = errors.New("account email does not match the invitation")
	// ErrAlreadyMember 表示账号已是该工作区的成员。
	ErrAlreadyMember = errors.New("account is already a member of the workspace")
)

// ValidationCode 标识邀请字段的校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationEmailInvalid       ValidationCode = "INVITATION_EMAIL_INVALID"
	ValidationEmailMember        ValidationCode = "INVITATION_EMAIL_MEMBER"
	ValidationEmailPending       ValidationCode = "INVITATION_EMAIL_PENDING"
	ValidationDisplayNameInvalid ValidationCode = "INVITATION_DISPLAY_NAME_INVALID"
	ValidationRoleInvalid        ValidationCode = "INVITATION_ROLE_INVALID"
)

// ValidationError 表示邀请字段校验失败。
type ValidationError = common.FieldError

// Mailer 发送邀请邮件，部署未配置邮件发送时为空。
type Mailer interface {
	Send(ctx context.Context, message mail.Message) error
}

// Invitation 描述工作区中的一条邀请；待接受但已过期的邀请状态为 expired。
type Invitation struct {
	ID            string                  `bun:"id"`
	InvitedEmail  string                  `bun:"invited_email"`
	DisplayName   string                  `bun:"display_name"`
	RoleID        string                  `bun:"role_id"`
	RoleKind      domain.RoleKind         `bun:"role_kind"`
	RoleName      string                  `bun:"role_name"`
	Status        domain.InvitationStatus `bun:"status"`
	ExpiresAt     time.Time               `bun:"expires_at"`
	CreatedAt     time.Time               `bun:"created_at"`
	InviterName   string                  `bun:"inviter_name"`
	EmailDelivery bool                    `bun:"-"`
}

// Created 返回新建的邀请及只返回一次的邀请链接。
type Created struct {
	Invitation Invitation
	Link       string
}

// Link 返回部署地址下接受邀请的链接，令牌位于片段中，不进入服务端访问日志。
func Link(publicURL, value string) string {
	return strings.TrimRight(publicURL, "/") + "/#/invitations/" + value
}

// selectInvitations 构造读取邀请、角色和发起人名称的查询。
func selectInvitations(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().TableExpr("organization_invitations AS inv").
		ColumnExpr("inv.id::text AS id, inv.invited_email, inv.display_name, inv.role_id::text AS role_id, COALESCE(r.kind, '') AS role_kind, COALESCE(r.name, '') AS role_name").
		ColumnExpr("CASE WHEN inv.status = ? AND inv.expires_at <= now() THEN ? ELSE inv.status END AS status", domain.InvitationStatusPending, domain.InvitationStatusExpired).
		ColumnExpr("inv.expires_at, inv.created_at, COALESCE(ioi.display_name, '') AS inviter_name").
		// 角色被删除时邀请仍需出现在列表中以便撤销。
		Join("LEFT JOIN roles AS r ON r.id = inv.role_id AND r.organization_id = inv.organization_id").
		Join("LEFT JOIN users AS iu ON iu.id = inv.invited_by_user_id AND iu.organization_id = inv.organization_id").
		Join("LEFT JOIN organization_identities AS ioi ON ioi.id = iu.identity_id AND ioi.organization_id = iu.organization_id")
}

// insertInvitation 在调用方事务内写入新邀请并返回令牌明文；同一邮箱已有待接受邀请时返回邮箱校验错误。
func insertInvitation(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, email, displayName, roleID string) (string, string, error) {
	issued, err := token.Issue(domain.InvitationValidity)
	if err != nil {
		return "", "", err
	}
	record := &servermodels.OrganizationInvitation{
		OrganizationID:  identity.Organization.ID,
		InvitedEmail:    email,
		DisplayName:     displayName,
		RoleID:          roleID,
		TokenHash:       issued.TokenHash,
		Status:          string(domain.InvitationStatusPending),
		ExpiresAt:       issued.ExpiresAt,
		InvitedByUserID: identity.User.ID,
	}
	if _, err := tx.NewInsert().Model(record).
		Column("organization_id", "invited_email", "display_name", "role_id", "token_hash", "status", "expires_at", "invited_by_user_id").
		Returning("id").
		Exec(ctx); err != nil {
		return "", "", err
	}
	return record.ID, issued.Token, nil
}

// loadInvitation 读取当前工作区中的一条邀请。
func loadInvitation(ctx context.Context, db bun.IDB, organizationID, invitationID string) (Invitation, error) {
	var invitation Invitation
	err := selectInvitations(db).
		Where("inv.organization_id = ?", organizationID).
		Where("inv.id = ?", invitationID).
		Scan(ctx, &invitation)
	return invitation, err
}
