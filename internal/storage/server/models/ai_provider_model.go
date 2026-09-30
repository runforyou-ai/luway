//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// AIProviderModel 表示 PostgreSQL 中供应商模型目录项。
type AIProviderModel struct {
	bun.BaseModel `bun:"table:ai_provider_models,alias:aipm"`

	ProviderID      string          `bun:"provider_id,pk"`
	OrganizationID  string          `bun:"organization_id"`
	Identifier      string          `bun:"identifier,pk"`
	Name            string          `bun:"name"`
	Type            string          `bun:"model_type"`
	InputModalities json.RawMessage `bun:"input_modalities,type:jsonb"`
	ContextWindow   int64           `bun:"context_window"`
	MaxOutputTokens int64           `bun:"max_output_tokens"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at,nullzero,default:now()"`
}
