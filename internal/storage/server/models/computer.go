//go:build server

package models

import (
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// Computer 表示为 AI 员工执行文件、命令与本机 MCP 操作的电脑：成员的个人电脑或工作区电脑，工作区电脑没有主人与安装标识。
type Computer struct {
	bun.BaseModel `bun:"table:computers,alias:cmp"`

	ID              string                      `bun:"id,pk"`
	WorkspaceID     string                      `bun:"workspace_id"`
	Kind            domain.ComputerKind         `bun:"kind"`
	OwnerUserID     *string                     `bun:"owner_user_id"`
	InstallID       *string                     `bun:"install_id"`
	Name            string                      `bun:"name"`
	Platform        *domain.ComputerPlatform    `bun:"platform"`
	CredentialHash  string                      `bun:"credential_hash"`
	Capabilities    domain.ComputerCapabilities `bun:"capabilities,type:jsonb"`
	MaxConcurrency  int                         `bun:"max_concurrency"`
	ExecutorVersion string                      `bun:"executor_version"`
	LastSeenAt      *time.Time                  `bun:"last_seen_at"`
	RevokedAt       *time.Time                  `bun:"revoked_at"`
	CreatedAt       time.Time                   `bun:"created_at"`
	UpdatedAt       time.Time                   `bun:"updated_at"`
}

// ComputerOnlineExpr 返回判断电脑在线的 SQL 布尔表达式：电脑未撤销且在在线时限内收到已认证 HTTP 心跳；alias 是电脑表的别名，没有关联到电脑时为假。
func ComputerOnlineExpr(alias string) string {
	return fmt.Sprintf("(%[1]s.revoked_at IS NULL AND COALESCE(%[1]s.last_seen_at > now() - make_interval(secs => %[2]d), false))", alias, int(domain.ComputerPresenceTimeout.Seconds()))
}
