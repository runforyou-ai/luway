//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChannelConnection 表示渠道长连接的连接状态，由持有渠道任务路由租约的服务端实例写入。
type ChannelConnection struct {
	bun.BaseModel `bun:"table:channel_connections,alias:chc"`

	CreatedAt   time.Time  `bun:"created_at"`
	UpdatedAt   time.Time  `bun:"updated_at"`
	ChannelID   string     `bun:"channel_id,pk"`
	WorkspaceID string     `bun:"workspace_id"`
	Status      string     `bun:"status"`
	LastError   string     `bun:"last_error"`
	ConnectedAt *time.Time `bun:"connected_at"`
}
