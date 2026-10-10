//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Account 表示 PostgreSQL 中平台内的登录账号。
type Account struct {
	bun.BaseModel `bun:"table:accounts,alias:acc"`

	ID              string     `bun:"id,pk"`
	Email           string     `bun:"email"`
	EmailVerifiedAt *time.Time `bun:"email_verified_at"`
	PasswordHash    string     `bun:"password_hash,nullzero"`
	DisplayName     string     `bun:"display_name"`
	Locale          string     `bun:"locale"`
	TimeZone        string     `bun:"time_zone"`
	Status          string     `bun:"status"`
	IsPlatformAdmin bool       `bun:"is_platform_admin"`
	CreatedAt       time.Time  `bun:"created_at"`
	UpdatedAt       time.Time  `bun:"updated_at"`
}

// AccountSession 表示 PostgreSQL 中的账号登录会话。
type AccountSession struct {
	bun.BaseModel `bun:"table:account_sessions,alias:acs"`

	ID        string    `bun:"id,pk"`
	AccountID string    `bun:"account_id"`
	TokenHash string    `bun:"token_hash"`
	ExpiresAt time.Time `bun:"expires_at"`
	// PushApp、PushPlatform 与 PushDeviceID 是本设备的离线推送目标，未注册时为空。
	PushApp      *string   `bun:"push_app"`
	PushPlatform *string   `bun:"push_platform"`
	PushDeviceID *string   `bun:"push_device_id"`
	CreatedAt    time.Time `bun:"created_at"`
	UpdatedAt    time.Time `bun:"updated_at,nullzero,default:now()"`
	// Remaining 是解析登录会话时按数据库时刻算出的剩余有效期。
	Remaining time.Duration `bun:"-"`
}
