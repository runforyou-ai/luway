//go:build !server && !ios && !android

// Package models 定义桌面端专有 SQLite 表的 Bun 数据模型。
package models

import "github.com/uptrace/bun"

// ComputerRegistration 表示本机为一个账号在一个工作区注册的电脑及其电脑凭据。
type ComputerRegistration struct {
	bun.BaseModel `bun:"table:computer_registrations,alias:computer_registration"`

	ServerURL      string `bun:"server_url,pk"`
	AccountID      string `bun:"account_id,pk"`
	OrganizationID string `bun:"organization_id,pk"`
	ComputerID     string `bun:"computer_id"`
	Credential     string `bun:"credential"`
	RegisteredAt   string `bun:"registered_at"`
}
