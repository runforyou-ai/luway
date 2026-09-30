//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// MessageMention 表示消息对聊天主体的结构化提醒关系。
type MessageMention struct {
	bun.BaseModel `bun:"table:message_mentions,alias:mm"`

	ID             string    `bun:"id,pk"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
	OrganizationID string    `bun:"organization_id"`
	MessageID      string    `bun:"message_id"`
	SubjectID      string    `bun:"subject_id"`
}
