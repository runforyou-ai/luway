//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// MessageTranslation 表示客户会话消息的一份译文。
type MessageTranslation struct {
	bun.BaseModel `bun:"table:message_translations,alias:mt"`

	MessageID      string    `bun:"message_id,pk"`
	Language       string    `bun:"language,pk"`
	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
	OrganizationID string    `bun:"organization_id"`
	Body           string    `bun:"body"`
	Authored       bool      `bun:"authored"`
}
