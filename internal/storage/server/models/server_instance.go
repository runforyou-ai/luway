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

	ID                    string    `bun:"id,pk"`
	StartedAt             time.Time `bun:"started_at,nullzero,default:now()"`
	HeartbeatAt           time.Time `bun:"heartbeat_at,nullzero,default:now()"`
	Hostname              string    `bun:"hostname"`
	Version               string    `bun:"version"`
	TasksNATSConnected    bool      `bun:"tasks_nats_connected"`
	RealtimeNATSConnected bool      `bun:"realtime_nats_connected"`
	// Config 是进程启动时可写入诊断信息的服务端配置。
	Config serverconfig.Diagnostics `bun:"config,type:jsonb"`
}
