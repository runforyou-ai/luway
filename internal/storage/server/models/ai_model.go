//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// AIModel 表示 PostgreSQL 中的 AI 模型，OrganizationID 为空表示平台模型。
type AIModel struct {
	bun.BaseModel `bun:"table:ai_models,alias:aim"`

	ID                 string          `bun:"id,pk"`
	OrganizationID     *string         `bun:"organization_id"`
	Name               string          `bun:"name"`
	Type               string          `bun:"model_type"`
	InputModalities    json.RawMessage `bun:"input_modalities,type:jsonb"`
	ContextWindow      int64           `bun:"context_window"`
	MaxOutputTokens    int64           `bun:"max_output_tokens"`
	InputCreditPrice   *int64          `bun:"input_credit_price"`
	OutputCreditPrice  *int64          `bun:"output_credit_price"`
	RequestCreditPrice *int64          `bun:"request_credit_price"`
	CreatedAt          time.Time       `bun:"created_at,nullzero,default:now()"`
	UpdatedAt          time.Time       `bun:"updated_at,nullzero,default:now()"`
}

// AIModelRoute 表示 PostgreSQL 中的 AI 模型来源路由。
type AIModelRoute struct {
	bun.BaseModel `bun:"table:ai_model_routes,alias:amr"`

	ID         string    `bun:"id,pk"`
	ModelID    string    `bun:"model_id"`
	ProviderID string    `bun:"provider_id"`
	Identifier string    `bun:"identifier"`
	Priority   int       `bun:"priority"`
	Enabled    bool      `bun:"enabled"`
	CreatedAt  time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt  time.Time `bun:"updated_at,nullzero,default:now()"`
}
