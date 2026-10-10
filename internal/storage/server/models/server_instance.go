//go:build server

package models

import (
	"time"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/uptrace/bun"
)

// ServerInstance 表示一个运行中的服务端进程及其最近一次心跳。
type ServerInstance struct {
	bun.BaseModel `bun:"table:server_instances,alias:si"`

	ID          string    `bun:"id,pk"`
	StartedAt   time.Time `bun:"started_at,nullzero,default:now()"`
	HeartbeatAt time.Time `bun:"heartbeat_at,nullzero,default:now()"`
	CreatedAt   time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt   time.Time `bun:"updated_at,nullzero,default:now()"`
	Hostname    string    `bun:"hostname"`
	Version     string    `bun:"version"`
	// BusDriver 是消息总线的传输驱动，取值同 clusterbus 的驱动名称。
	BusDriver    string `bun:"bus_driver"`
	BusConnected bool   `bun:"bus_connected"`
	// BusMessageRate 是上一个心跳间隔内经消息总线发送的消息数（条/秒），BusFailures 是同一间隔内发送失败与丢弃的消息数。
	BusMessageRate float64 `bun:"bus_message_rate"`
	BusFailures    int     `bun:"bus_failures"`
	// Config 是进程启动时可写入诊断信息的服务端配置。
	Config serverconfig.Diagnostics `bun:"config,type:jsonb"`
}
