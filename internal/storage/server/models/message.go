//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// Message 表示会话消息。
type Message struct {
	bun.BaseModel `bun:"table:messages,alias:msg"`

	ID                  string          `bun:"id,pk"`
	CreatedAt           time.Time       `bun:"created_at"`
	UpdatedAt           time.Time       `bun:"updated_at"`
	WorkspaceID         string          `bun:"workspace_id"`
	ConversationID      string          `bun:"conversation_id"`
	ServiceSessionID    *string         `bun:"service_session_id"`
	SenderParticipantID *string         `bun:"sender_participant_id"`
	Type                string          `bun:"type"`
	Visibility          string          `bun:"visibility"`
	Body                string          `bun:"body"`
	Language            *string         `bun:"language"`
	SearchVector        string          `bun:"search_vector"`
	SystemEventType     *string         `bun:"system_event_type"`
	SystemEventPayload  json.RawMessage `bun:"system_event_payload,type:jsonb"`
	ReplyToMessageID    *string         `bun:"reply_to_message_id"`
	MentionAll          bool            `bun:"mention_all"`
	ClientMessageID     *string         `bun:"client_message_id"`
	IdempotencyKey      *string         `bun:"idempotency_key"`
	OriginatedAt        time.Time       `bun:"originated_at"`
	MessageSeq          int64           `bun:"message_seq"`
	EditedAt            *time.Time      `bun:"edited_at"`
	DeletedAt           *time.Time      `bun:"deleted_at"`
	// AgentToolCallID 是产生该消息的工具调用编号，本机 Agent 的回复关联委派的一轮。
	AgentToolCallID *string `bun:"agent_tool_call_id"`
}
