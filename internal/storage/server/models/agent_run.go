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
	OrganizationID    string          `bun:"organization_id"`
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
	Usage             json.RawMessage `bun:"usage,type:jsonb"`
	BehaviorSnapshot  json.RawMessage `bun:"behavior_snapshot,type:jsonb,nullzero"`
	Plan              json.RawMessage `bun:"plan,type:jsonb,nullzero"`
	Outcome           *string         `bun:"outcome"`
	OutcomeReason     *string         `bun:"outcome_reason"`
	HandoffSettledSeq *int64          `bun:"handoff_settled_seq"`
	LastError         *string         `bun:"last_error"`
	ErrorCode         *string         `bun:"error_code"`
	StartedAt         *time.Time      `bun:"started_at"`
	CompletedAt       *time.Time      `bun:"completed_at"`
	ExecutionDeviceID *string         `bun:"execution_device_id"`
	ClaimedAt         *time.Time      `bun:"claimed_at"`
	LeaseExpiresAt    *time.Time      `bun:"lease_expires_at"`
	CreatedAt         time.Time       `bun:"created_at"`
	UpdatedAt         time.Time       `bun:"updated_at"`
}
