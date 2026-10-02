//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Deployment 表示 PostgreSQL 中唯一一行的部署实例与部署级策略。
type Deployment struct {
	bun.BaseModel `bun:"table:deployments,alias:dep"`

	InstanceID              string    `bun:"instance_id,pk"`
	CreatedAt               time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt               time.Time `bun:"updated_at,nullzero,default:now()"`
	RegistrationPolicy      string    `bun:"registration_policy"`
	WorkspaceCreationPolicy string    `bun:"workspace_creation_policy"`
}
