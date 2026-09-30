//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Account 表示 PostgreSQL 中部署内的登录账号。
type Account struct {
	bun.BaseModel `bun:"table:accounts,alias:acc"`

	ID                string     `bun:"id,pk"`
	Email             string     `bun:"email"`
	EmailVerifiedAt   *time.Time `bun:"email_verified_at"`
	PasswordHash      string     `bun:"password_hash,nullzero"`
	DisplayName       string     `bun:"display_name"`
	Locale            string     `bun:"locale"`
	TimeZone          string     `bun:"time_zone"`
	Status            string     `bun:"status"`
	IsDeploymentAdmin bool       `bun:"is_deployment_admin"`
	CreatedAt         time.Time  `bun:"created_at"`
	UpdatedAt         time.Time  `bun:"updated_at"`
}

// AccountSession 表示 PostgreSQL 中的账号登录会话。
type AccountSession struct {
	bun.BaseModel `bun:"table:account_sessions,alias:acs"`

	ID        string    `bun:"id,pk"`
	AccountID string    `bun:"account_id"`
	TokenHash string    `bun:"token_hash"`
	ExpiresAt time.Time `bun:"expires_at"`
	CreatedAt time.Time `bun:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,nullzero,default:now()"`
}

// AccountExternalIdentity 表示账号与官方身份服务账号的绑定。
type AccountExternalIdentity struct {
	bun.BaseModel `bun:"table:account_external_identities,alias:aei"`

	ID        string    `bun:"id,pk"`
	AccountID string    `bun:"account_id"`
	Issuer    string    `bun:"issuer"`
	Subject   string    `bun:"subject"`
	CreatedAt time.Time `bun:"created_at"`
	UpdatedAt time.Time `bun:"updated_at"`
}
