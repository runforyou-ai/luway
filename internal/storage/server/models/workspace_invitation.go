//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WorkspaceInvitation 表示 PostgreSQL 中的工作区成员邀请。
type WorkspaceInvitation struct {
	bun.BaseModel `bun:"table:workspace_invitations,alias:inv"`

	ID                string     `bun:"id,pk"`
	WorkspaceID       string     `bun:"workspace_id"`
	InvitedEmail      string     `bun:"invited_email"`
	DisplayName       string     `bun:"display_name"`
	RoleID            string     `bun:"role_id"`
	TokenHash         string     `bun:"token_hash"`
	EmailTokenHash    *string    `bun:"email_token_hash"`
	Status            string     `bun:"status"`
	ExpiresAt         time.Time  `bun:"expires_at"`
	InvitedByUserID   string     `bun:"invited_by_user_id"`
	AcceptedAccountID *string    `bun:"accepted_account_id"`
	AcceptedUserID    *string    `bun:"accepted_user_id"`
	AcceptedAt        *time.Time `bun:"accepted_at"`
	CreatedAt         time.Time  `bun:"created_at"`
	UpdatedAt         time.Time  `bun:"updated_at"`
}
