//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ConversationMentionReview 保存连续水位之后已单独查看的提及。
type ConversationMentionReview struct {
	bun.BaseModel `bun:"table:conversation_mention_reviews,alias:cmr"`

	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
	OrganizationID string    `bun:"organization_id,pk"`
	ConversationID string    `bun:"conversation_id,pk"`
	UserID         string    `bun:"user_id,pk"`
	MessageID      string    `bun:"message_id,pk"`
}
