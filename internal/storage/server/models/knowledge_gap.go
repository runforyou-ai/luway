//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// KnowledgeGap 表示待整理为知识库问答的客服处理周期及 AI 起草的问答。
type KnowledgeGap struct {
	bun.BaseModel `bun:"table:knowledge_gaps,alias:kg"`

	ID                    string     `bun:"id,pk"`
	CreatedAt             time.Time  `bun:"created_at"`
	UpdatedAt             time.Time  `bun:"updated_at"`
	OrganizationID        string     `bun:"organization_id"`
	ServiceSessionID      string     `bun:"service_session_id"`
	ConversationID        string     `bun:"conversation_id"`
	Source                string     `bun:"source"`
	TriggerMessageID      string     `bun:"trigger_message_id"`
	OccurredAt            time.Time  `bun:"occurred_at"`
	QuestionMessageID     *string    `bun:"question_message_id"`
	Status                string     `bun:"status"`
	DraftStatus           string     `bun:"draft_status"`
	DraftRequestedAt      time.Time  `bun:"draft_requested_at"`
	DraftQuestion         *string    `bun:"draft_question"`
	DraftSimilarQuestions []string   `bun:"draft_similar_questions,type:jsonb"`
	DraftAnswer           *string    `bun:"draft_answer"`
	KnowledgeBaseID       *string    `bun:"knowledge_base_id"`
	QAEntryID             *string    `bun:"qa_entry_id"`
	HandledByIdentityID   *string    `bun:"handled_by_identity_id"`
	HandledAt             *time.Time `bun:"handled_at"`
}
