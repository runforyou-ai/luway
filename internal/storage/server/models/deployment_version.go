//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// DeploymentVersion 表示部署当前运行的服务端版本。
type DeploymentVersion struct {
	bun.BaseModel `bun:"table:deployment_versions,alias:dv"`

	Version   string    `bun:"version,pk"`
	CreatedAt time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt time.Time `bun:"updated_at,nullzero,default:now()"`
}
