//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// AgentRun 表示一次可吸收多条用户输入的 Agent 业务运行。
type AgentRun struct {
	bun.BaseModel `bun:"table:agent_runs,alias:agr"`

	ID                string          `bun:"id,pk"`
	WorkspaceID       string          `bun:"workspace_id"`
	ConversationID    string          `bun:"conversation_id"`
	AgentIdentityID   string          `bun:"agent_identity_id"`
	AgentRevisionID   string          `bun:"agent_revision_id"`
	LaneID            string          `bun:"lane_id"`
	ScopeKind         string          `bun:"scope_kind"`
	ScopeID           string          `bun:"scope_id"`
	Status            string          `bun:"status"`
	InputStartSeq     int64           `bun:"input_start_seq"`
	InputEndSeq       *int64          `bun:"input_end_seq"`
	ResponseMessageID *string         `bun:"response_message_id"`
	Usage             json.RawMessage `bun:"usage,type:jsonb"` // 运行读模型中的用量，投影自该运行的对话模型调用记录。
	BehaviorSnapshot  json.RawMessage `bun:"behavior_snapshot,type:jsonb,nullzero"`
	State             []byte          `bun:"state,nullzero"`
	TaskRunID         *string         `bun:"task_run_id"`
	TaskAttempt       int             `bun:"task_attempt"`
	TaskInstanceID    *string         `bun:"task_instance_id"`
	Plan              json.RawMessage `bun:"plan,type:jsonb,nullzero"`
	// Completion 是运行当前生效的完成，没有时为空。
	Completion        json.RawMessage `bun:"completion,type:jsonb,nullzero"`
	Outcome           *string         `bun:"outcome"`
	OutcomeReason     *string         `bun:"outcome_reason"`
	HandoffSettledSeq *int64          `bun:"handoff_settled_seq"`
	LastError         *string         `bun:"last_error"`
	ErrorCode         *string         `bun:"error_code"`
	StartedAt         *time.Time      `bun:"started_at"`
	CompletedAt       *time.Time      `bun:"completed_at"`
	CreatedAt         time.Time       `bun:"created_at"`
	UpdatedAt         time.Time       `bun:"updated_at"`
}
