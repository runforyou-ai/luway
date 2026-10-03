//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// AIModelCall 表示 PostgreSQL 中的 AI 模型调用记录。
type AIModelCall struct {
	bun.BaseModel `bun:"table:ai_model_calls,alias:amc"`

	ID                 string     `bun:"id,pk"`
	CreatedAt          time.Time  `bun:"created_at,nullzero,default:now()"`
	FinishedAt         *time.Time `bun:"finished_at"`
	OrganizationID     string     `bun:"organization_id"`
	ModelID            string     `bun:"model_id"`
	ModelName          string     `bun:"model_name"`
	ModelUsage         string     `bun:"model_usage"`
	ModelScope         string     `bun:"model_scope"`
	ActorType          string     `bun:"actor_type"`
	ActorID            *string    `bun:"actor_id"`
	SourceType         string     `bun:"source_type"`
	SourceID           *string    `bun:"source_id"`
	Status             string     `bun:"status"`
	InputTokens        int64      `bun:"input_tokens"`
	CachedInputTokens  int64      `bun:"cached_input_tokens"`
	OutputTokens       int64      `bun:"output_tokens"`
	ErrorMessage       string     `bun:"error_message"`
	InputCreditPrice   *int64     `bun:"input_credit_price"`
	OutputCreditPrice  *int64     `bun:"output_credit_price"`
	RequestCreditPrice *int64     `bun:"request_credit_price"`
	Credits            int64      `bun:"credits"`
	CreditShortfall    int64      `bun:"credit_shortfall"`
}

// AIModelCallAttempt 表示 PostgreSQL 中的 AI 模型上游尝试。
type AIModelCallAttempt struct {
	bun.BaseModel `bun:"table:ai_model_call_attempts,alias:amca"`

	ID                string     `bun:"id,pk"`
	CreatedAt         time.Time  `bun:"created_at,nullzero,default:now()"`
	FinishedAt        *time.Time `bun:"finished_at"`
	CallID            string     `bun:"call_id"`
	RouteID           string     `bun:"route_id"`
	ProviderID        string     `bun:"provider_id"`
	ProviderName      string     `bun:"provider_name"`
	Identifier        string     `bun:"identifier"`
	Status            string     `bun:"status"`
	InputTokens       int64      `bun:"input_tokens"`
	CachedInputTokens int64      `bun:"cached_input_tokens"`
	OutputTokens      int64      `bun:"output_tokens"`
	ErrorMessage      string     `bun:"error_message"`
}
