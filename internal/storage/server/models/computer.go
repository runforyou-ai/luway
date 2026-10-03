//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// Computer 表示为 AI 员工执行文件、命令与本机 MCP 操作的电脑。
type Computer struct {
	bun.BaseModel `bun:"table:computers,alias:cmp"`

	ID              string                      `bun:"id,pk"`
	OrganizationID  string                      `bun:"organization_id"`
	Kind            domain.ComputerKind         `bun:"kind"`
	OwnerUserID     *string                     `bun:"owner_user_id"`
	InstallID       string                      `bun:"install_id"`
	Name            string                      `bun:"name"`
	Platform        domain.ComputerPlatform     `bun:"platform"`
	CredentialHash  string                      `bun:"credential_hash"`
	Capabilities    domain.ComputerCapabilities `bun:"capabilities,type:jsonb"`
	MaxConcurrency  int                         `bun:"max_concurrency"`
	ExecutorVersion string                      `bun:"executor_version"`
	LastSeenAt      *time.Time                  `bun:"last_seen_at"`
	RevokedAt       *time.Time                  `bun:"revoked_at"`
	CreatedAt       time.Time                   `bun:"created_at"`
	UpdatedAt       time.Time                   `bun:"updated_at"`
}

// Online 判断电脑未撤销且事件流在在线时限内记录过在线。
func (c *Computer) Online(now time.Time) bool {
	return c.RevokedAt == nil && c.LastSeenAt != nil && now.Sub(*c.LastSeenAt) < domain.ComputerPresenceTimeout
}
