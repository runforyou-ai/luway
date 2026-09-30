//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ConversationParticipant 表示会话参与者关系。
type ConversationParticipant struct {
	bun.BaseModel `bun:"table:conversation_participants,alias:cp"`

	ID             string     `bun:"id,pk"`
	CreatedAt      time.Time  `bun:"created_at"`
	UpdatedAt      time.Time  `bun:"updated_at"`
	OrganizationID string     `bun:"organization_id"`
	ConversationID string     `bun:"conversation_id"`
	SubjectID      string     `bun:"subject_id"`
	Role           string     `bun:"role"`
	JoinedAt       time.Time  `bun:"joined_at"`
	LeftAt         *time.Time `bun:"left_at"`
}
