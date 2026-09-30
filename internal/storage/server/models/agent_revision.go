//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// AgentRevision 表示 PostgreSQL 中不可变的 AI 员工执行配置。
type AgentRevision struct {
	bun.BaseModel `bun:"table:agent_revisions,alias:ar"`

	ID              string          `bun:"id,pk"`
	OrganizationID  string          `bun:"organization_id"`
	AgentID         string          `bun:"agent_id"`
	ExecutionMode   string          `bun:"execution_mode"`
	SchemaVersion   int             `bun:"schema_version"`
	Configuration   json.RawMessage `bun:"configuration,type:jsonb"`
	CreatedByUserID string          `bun:"created_by_user_id"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at,nullzero,default:now()"`
}
