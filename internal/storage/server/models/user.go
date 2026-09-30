//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// User 表示 PostgreSQL 中的工作区成员。
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`

	ID                          string     `bun:"id,pk"`
	IdentityID                  string     `bun:"identity_id"`
	OrganizationID              string     `bun:"organization_id"`
	AccountID                   string     `bun:"account_id"`
	RoleID                      string     `bun:"role_id"`
	Status                      string     `bun:"status"`
	TranslationLanguage         *string    `bun:"translation_language"`
	MessageNotificationsEnabled bool       `bun:"message_notifications_enabled"`
	ProfileVersion              int64      `bun:"profile_version"`
	PinOrderVersion             int64      `bun:"pin_order_version"`
	MaxServiceSessions          int        `bun:"max_service_sessions"`
	LastServiceAssignedAt       *time.Time `bun:"last_service_assigned_at"`
	CreatedAt                   time.Time  `bun:"created_at"`
	UpdatedAt                   time.Time  `bun:"updated_at"`
}
