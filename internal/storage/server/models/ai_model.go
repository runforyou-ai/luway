//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// AIModel 表示 PostgreSQL 中的 AI 模型，作用域以所属供应商为准。
type AIModel struct {
	bun.BaseModel `bun:"table:ai_models,alias:aim"`

	ID              string          `bun:"id,pk"`
	ProviderID      string          `bun:"provider_id"`
	Identifier      string          `bun:"identifier"`
	Name            string          `bun:"name"`
	Type            string          `bun:"model_type"`
	InputModalities json.RawMessage `bun:"input_modalities,type:jsonb"`
	ContextWindow   int64           `bun:"context_window"`
	MaxOutputTokens int64           `bun:"max_output_tokens"`
	CreatedAt       time.Time       `bun:"created_at,nullzero,default:now()"`
	UpdatedAt       time.Time       `bun:"updated_at,nullzero,default:now()"`
}
