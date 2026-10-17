//go:build server

// Package models 定义 PostgreSQL 使用的 Bun 数据模型。
package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Workspace 表示 PostgreSQL 中的工作区。
type Workspace struct {
	bun.BaseModel `bun:"table:workspaces,alias:o"`

	ID                 string    `bun:"id,pk"`
	Slug               string    `bun:"slug"`
	Name               string    `bun:"name"`
	LifecycleStatus    string    `bun:"lifecycle_status"`
	LastContactNumber  int64     `bun:"last_contact_number"`
	CreatedByAccountID string    `bun:"created_by_account_id,nullzero"`
	CreatedAt          time.Time `bun:"created_at"`
	UpdatedAt          time.Time `bun:"updated_at"`
}
