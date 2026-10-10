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
	WorkspaceID     string          `bun:"workspace_id"`
	AgentID         string          `bun:"agent_id"`
	ExecutionMode   string          `bun:"execution_mode"`
	ModelID         string          `bun:"model_id,nullzero"`
	SchemaVersion   int             `bun:"schema_version"`
	Configuration   json.RawMessage `bun:"configuration,type:jsonb"`
	CreatedByUserID string          `bun:"created_by_user_id"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at,nullzero,default:now()"`
}

// AgentRevisionKnowledgeBase 表示 AI 员工配置版本绑定的知识库。
type AgentRevisionKnowledgeBase struct {
	bun.BaseModel `bun:"table:agent_revision_knowledge_bases,alias:arkb"`

	ID              string    `bun:"id,pk"`
	WorkspaceID     string    `bun:"workspace_id"`
	AgentID         string    `bun:"agent_id"`
	RevisionID      string    `bun:"revision_id"`
	KnowledgeBaseID string    `bun:"knowledge_base_id"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at"`
}

// AgentRevisionBusinessSystem 表示 AI 员工配置版本授权的业务系统。
type AgentRevisionBusinessSystem struct {
	bun.BaseModel `bun:"table:agent_revision_business_systems,alias:arbs"`

	ID               string    `bun:"id,pk"`
	WorkspaceID      string    `bun:"workspace_id"`
	AgentID          string    `bun:"agent_id"`
	RevisionID       string    `bun:"revision_id"`
	BusinessSystemID string    `bun:"business_system_id"`
	CreatedAt        time.Time `bun:"created_at"`
	UpdatedAt        time.Time `bun:"updated_at"`
}
