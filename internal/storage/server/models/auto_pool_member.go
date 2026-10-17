//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// AutoPoolMember 表示 PostgreSQL 中的平台 Auto 能力池成员，每条对应一个上游。
type AutoPoolMember struct {
	bun.BaseModel `bun:"table:auto_pool_members,alias:apm"`

	ID              string          `bun:"id,pk"`
	ProviderID      string          `bun:"provider_id"`
	Identifier      string          `bun:"identifier"`
	Type            string          `bun:"model_type"`
	InputModalities json.RawMessage `bun:"input_modalities,type:jsonb"`
	ContextWindow   int64           `bun:"context_window"`
	MaxOutputTokens int64           `bun:"max_output_tokens"`
	Priority        int             `bun:"priority"`
	Weight          int             `bun:"weight"`
	Enabled         bool            `bun:"enabled"`
	CreatedAt       time.Time       `bun:"created_at,nullzero,default:now()"`
	UpdatedAt       time.Time       `bun:"updated_at,nullzero,default:now()"`
}
