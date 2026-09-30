//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// AgentInput 表示一条等待 Agent 消费的持久输入。
type AgentInput struct {
	bun.BaseModel `bun:"table:agent_inputs,alias:ai"`

	ID              string    `bun:"id,pk"`
	OrganizationID  string    `bun:"organization_id"`
	LaneID          string    `bun:"lane_id"`
	InputSeq        int64     `bun:"input_seq"`
	Kind            string    `bun:"kind"`
	SourceMessageID string    `bun:"source_message_id"`
	SourceSubjectID string    `bun:"source_subject_id"`
	SourceOrdinal   int       `bun:"source_ordinal"`
	AgentRunID      *string   `bun:"agent_run_id"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at,nullzero,default:now()"`
}
