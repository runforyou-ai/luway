//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// LoginAttempt 表示一次官方账号登录尝试，授权码交换时一次性消费。
type LoginAttempt struct {
	bun.BaseModel `bun:"table:login_attempts,alias:la"`

	ID            string     `bun:"id,pk"`
	Purpose       string     `bun:"purpose"`
	ClientType    string     `bun:"client_type"`
	RedirectURI   string     `bun:"redirect_uri"`
	CodeChallenge string     `bun:"code_challenge"`
	Nonce         string     `bun:"nonce"`
	ExpiresAt     time.Time  `bun:"expires_at"`
	ConsumedAt    *time.Time `bun:"consumed_at"`
	CreatedAt     time.Time  `bun:"created_at"`
	UpdatedAt     time.Time  `bun:"updated_at"`
}
