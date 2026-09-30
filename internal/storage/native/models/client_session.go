//go:build !server

package models

import "github.com/uptrace/bun"

// ClientSession 表示原生端当前登录会话。
type ClientSession struct {
	bun.BaseModel `bun:"table:client_sessions,alias:client_session"`

	ID        string `bun:"id,pk"`
	ServerURL string `bun:"server_url"`
	AccountID string `bun:"account_id"`
	Token     string `bun:"token"`
	ExpiresAt string `bun:"expires_at"`
}
