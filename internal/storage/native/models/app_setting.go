//go:build !server

// Package models 定义桌面端与移动端共有 SQLite 表的 Bun 数据模型。
package models

import "github.com/uptrace/bun"

// AppSetting 表示原生端 SQLite 中的一项应用配置。
type AppSetting struct {
	bun.BaseModel `bun:"table:app_settings,alias:setting"`

	Key       string `bun:"key,pk"`
	Value     string `bun:"value"`
	UpdatedAt string `bun:"updated_at"`
}
