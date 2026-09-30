//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// AgentEvaluationCase 表示 AI 员工的一条评测用例。
type AgentEvaluationCase struct {
	bun.BaseModel `bun:"table:agent_evaluation_cases,alias:aec"`

	ID                  string          `bun:"id,pk"`
	CreatedAt           time.Time       `bun:"created_at"`
	UpdatedAt           time.Time       `bun:"updated_at"`
	OrganizationID      string          `bun:"organization_id"`
	AgentID             string          `bun:"agent_id"`
	Version             int             `bun:"version"`
	Audience            string          `bun:"audience"`
	Question            string          `bun:"question"`
	ExpectedAction      string          `bun:"expected_action"`
	ExpectedAnswer      string          `bun:"expected_answer"`
	CreatedByIdentityID string          `bun:"created_by_identity_id"`
	Source              string          `bun:"source"`
	ServiceSessionID    *string         `bun:"service_session_id"`
	QuestionMessageID   *string         `bun:"question_message_id"`
	OccurredAt          *time.Time      `bun:"occurred_at"`
	Context             json.RawMessage `bun:"context,type:jsonb"`
}

// AgentEvaluationRun 表示用一个配置版本对 AI 员工全部用例的一次评测运行。
type AgentEvaluationRun struct {
	bun.BaseModel `bun:"table:agent_evaluation_runs,alias:aer"`

	ID                  string     `bun:"id,pk"`
	CreatedAt           time.Time  `bun:"created_at"`
	UpdatedAt           time.Time  `bun:"updated_at"`
	OrganizationID      string     `bun:"organization_id"`
	AgentID             string     `bun:"agent_id"`
	AgentRevisionID     string     `bun:"agent_revision_id"`
	Status              string     `bun:"status"`
	CaseCount           int        `bun:"case_count"`
	PassedCount         int        `bun:"passed_count"`
	FailedCount         int        `bun:"failed_count"`
	ErrorCount          int        `bun:"error_count"`
	StartedByIdentityID string     `bun:"started_by_identity_id"`
	CompletedAt         *time.Time `bun:"completed_at"`
}

// AgentEvaluationResult 表示一次评测运行中一条用例的一次尝试。
type AgentEvaluationResult struct {
	bun.BaseModel `bun:"table:agent_evaluation_results,alias:aers"`

	ID                 string          `bun:"id,pk"`
	CreatedAt          time.Time       `bun:"created_at"`
	UpdatedAt          time.Time       `bun:"updated_at"`
	OrganizationID     string          `bun:"organization_id"`
	RunID              string          `bun:"run_id"`
	CaseID             string          `bun:"case_id"`
	Attempt            int             `bun:"attempt"`
	CaseVersion        int             `bun:"case_version"`
	CaseSnapshot       json.RawMessage `bun:"case_snapshot,type:jsonb"`
	Status             string          `bun:"status"`
	ActualAction       *string         `bun:"actual_action"`
	ActualReason       *string         `bun:"actual_reason"`
	Answer             string          `bun:"answer"`
	Blocks             json.RawMessage `bun:"blocks,type:jsonb"`
	CorrectProbability *float64        `bun:"correct_probability"`
	Usage              json.RawMessage `bun:"usage,type:jsonb"`
	Error              *string         `bun:"error"`
	CompletedAt        *time.Time      `bun:"completed_at"`
}
