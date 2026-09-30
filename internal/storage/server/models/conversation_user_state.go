//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ConversationUserState 表示用户在会话中的个人阅读、提醒、置顶与归档状态。
type ConversationUserState struct {
	bun.BaseModel `bun:"table:conversation_user_states,alias:cus"`

	ReadSeq                      int64      `bun:"read_seq"`
	ID                           string     `bun:"id,pk"`
	CreatedAt                    time.Time  `bun:"created_at"`
	UpdatedAt                    time.Time  `bun:"updated_at"`
	OrganizationID               string     `bun:"organization_id"`
	ConversationID               string     `bun:"conversation_id"`
	UserID                       string     `bun:"user_id"`
	LastReviewedMentionMessageID *string    `bun:"last_reviewed_mention_message_id"`
	LastReadMessageID            *string    `bun:"last_read_message_id"`
	LastReadAt                   *time.Time `bun:"last_read_at"`
	MarkedUnread                 bool       `bun:"marked_unread"`
	Version                      int64      `bun:"version"`
	PinRank                      *int64     `bun:"pin_rank"`
	Muted                        bool       `bun:"muted"`
	ArchivedAt                   *time.Time `bun:"archived_at"`
}
