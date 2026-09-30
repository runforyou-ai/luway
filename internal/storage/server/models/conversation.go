//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Conversation 表示聊天会话。
type Conversation struct {
	bun.BaseModel `bun:"table:conversations,alias:cv"`

	ID                 string     `bun:"id,pk"`
	CreatedAt          time.Time  `bun:"created_at"`
	UpdatedAt          time.Time  `bun:"updated_at"`
	OrganizationID     string     `bun:"organization_id"`
	Type               string     `bun:"type"`
	Status             string     `bun:"status"`
	Title              *string    `bun:"title"`
	Description        *string    `bun:"description"`
	ImageFileID        *string    `bun:"image_file_id"`
	CreatedBySubjectID *string    `bun:"created_by_subject_id"`
	LastMessageSeq     int64      `bun:"last_message_seq"`
	LastMessageID      *string    `bun:"last_message_id"`
	LastMessageAt      *time.Time `bun:"last_message_at"`
	LastActivityAt     *time.Time `bun:"last_activity_at"`
	// LastInternalActivityAt 是服务会话内部消息的最后追加活动时间。
	LastInternalActivityAt *time.Time `bun:"last_internal_activity_at"`
	Version                int64      `bun:"version"`
}
