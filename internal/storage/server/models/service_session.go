//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"

	"github.com/uptrace/bun"
)

// ServiceSession 表示服务会话中的一个服务周期。
type ServiceSession struct {
	bun.BaseModel `bun:"table:service_sessions,alias:ss"`

	ID                    string                 `bun:"id,pk"`
	CreatedAt             time.Time              `bun:"created_at"`
	UpdatedAt             time.Time              `bun:"updated_at"`
	OrganizationID        string                 `bun:"organization_id"`
	ConversationID        string                 `bun:"conversation_id"`
	ServiceConversationID string                 `bun:"service_conversation_id"`
	Sequence              int64                  `bun:"sequence"`
	Status                string                 `bun:"status"`
	TeamID                *string                `bun:"team_id"`
	AssigneeIdentityID    *string                `bun:"assignee_identity_id"`
	AgentIdentityID       *string                `bun:"agent_identity_id"`
	OpeningMessageID      string                 `bun:"opening_message_id"`
	LastMessageID         string                 `bun:"last_message_id"`
	LastMessageAt         time.Time              `bun:"last_message_at"`
	AssignedAt            *time.Time             `bun:"assigned_at"`
	AssigneeAssignedAt    *time.Time             `bun:"assignee_assigned_at"`
	AwaitingReplySince    *time.Time             `bun:"awaiting_reply_since"`
	QueuedAt              *time.Time             `bun:"queued_at"`
	RemindedAt            *time.Time             `bun:"reminded_at"`
	FirstResponseAt       *time.Time             `bun:"first_response_at"`
	HumanRequestedAt      *time.Time             `bun:"human_requested_at"`
	HumanAssignedAt       *time.Time             `bun:"human_assigned_at"`
	HumanFirstResponseAt  *time.Time             `bun:"human_first_response_at"`
	HumanFirstResponseSec *int                   `bun:"human_first_response_seconds"`
	StatusChangedAt       time.Time              `bun:"status_changed_at"`
	ClosedAt              *time.Time             `bun:"closed_at"`
	ClosedByIdentityID    *string                `bun:"closed_by_identity_id"`
	CloseReason           *string                `bun:"close_reason"`
	ResolutionRequestedAt *time.Time             `bun:"resolution_requested_at"`
	CategoryID            *string                `bun:"category_id"`
	RatingResolved        *bool                  `bun:"rating_resolved"`
	RatingComment         *string                `bun:"rating_comment"`
	RatedAt               *time.Time             `bun:"rated_at"`
	VisitorContext        *domain.VisitorContext `bun:"visitor_context,type:jsonb"`
	SummaryStatus         *string                `bun:"summary_status"`
	Summary               *string                `bun:"summary"`
	Resolved              *bool                  `bun:"resolved"`
	SummaryEditedByID     *string                `bun:"summary_edited_by_identity_id"`
	SummaryEditedAt       *time.Time             `bun:"summary_edited_at"`
	HandoffMessageID      *string                `bun:"handoff_message_id"`
	HandoffSummary        *domain.HandoffSummary `bun:"handoff_summary,type:jsonb"`
}
