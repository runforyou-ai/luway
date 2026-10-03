//go:build server

package platform

import (
	"context"
	"fmt"
	"time"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	// InstanceHeartbeatInterval 是服务端进程刷新心跳的间隔。
	InstanceHeartbeatInterval = 30 * time.Second
	// InstanceOnlineWindow 是判断服务端进程在线的心跳时限，超过时限视为失联。
	InstanceOnlineWindow = 2 * time.Minute
	// instanceRetention 是失联服务端进程记录的保留时长。
	instanceRetention = time.Hour
)

// ServerInstance 定义一个服务端进程及其最近一次心跳：TasksNATSConnected 与 RealtimeNATSConnected 为后台任务与实时通知的 NATS 连接是否可用，Online 表示心跳在 InstanceOnlineWindow 之内，Config 为进程启动时的服务端配置。
type ServerInstance struct {
	ID                    string                   `bun:"id"`
	StartedAt             time.Time                `bun:"started_at"`
	HeartbeatAt           time.Time                `bun:"heartbeat_at"`
	Hostname              string                   `bun:"hostname"`
	Version               string                   `bun:"version"`
	TasksNATSConnected    bool                     `bun:"tasks_nats_connected"`
	RealtimeNATSConnected bool                     `bun:"realtime_nats_connected"`
	Online                bool                     `bun:"online"`
	Config                serverconfig.Diagnostics `bun:"config,type:jsonb"`
}

// InstanceReport 定义一次心跳上报的服务端进程信息，Config 只在登记时写入。
type InstanceReport struct {
	ID                    string
	Hostname              string
	Version               string
	TasksNATSConnected    bool
	RealtimeNATSConnected bool
	Config                serverconfig.Diagnostics
}

// ReportInstance 登记服务端进程或刷新其心跳，并删除失联超过保留时长的进程记录。
func ReportInstance(ctx context.Context, db bun.IDB, report InstanceReport) error {
	if _, err := db.NewInsert().Model(&servermodels.ServerInstance{
		ID: report.ID, Hostname: report.Hostname, Version: report.Version,
		TasksNATSConnected: report.TasksNATSConnected, RealtimeNATSConnected: report.RealtimeNATSConnected,
		Config: report.Config,
	}).
		On("CONFLICT (id) DO UPDATE").
		Set("heartbeat_at = now()").
		Set("tasks_nats_connected = EXCLUDED.tasks_nats_connected").
		Set("realtime_nats_connected = EXCLUDED.realtime_nats_connected").
		Exec(ctx); err != nil {
		return fmt.Errorf("report server instance: %w", err)
	}
	if _, err := db.NewDelete().Model((*servermodels.ServerInstance)(nil)).
		Where("heartbeat_at < now() - make_interval(secs => ?)", instanceRetention.Seconds()).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete lost server instances: %w", err)
	}
	return nil
}

// RemoveInstance 删除正常退出的服务端进程记录。
func RemoveInstance(ctx context.Context, db bun.IDB, id string) error {
	if _, err := db.NewDelete().Model((*servermodels.ServerInstance)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("remove server instance: %w", err)
	}
	return nil
}

// listInstances 按启动时间返回保留中的服务端进程，在线的排在前面。
func listInstances(ctx context.Context, db bun.IDB) ([]ServerInstance, error) {
	instances := make([]ServerInstance, 0)
	if err := db.NewSelect().Model((*servermodels.ServerInstance)(nil)).
		ColumnExpr("si.id::text AS id, si.started_at, si.heartbeat_at, si.hostname, si.version, si.tasks_nats_connected, si.realtime_nats_connected, si.config").
		ColumnExpr("si.heartbeat_at >= now() - make_interval(secs => ?) AS online", InstanceOnlineWindow.Seconds()).
		OrderExpr("online DESC, si.started_at ASC, si.id ASC").
		Scan(ctx, &instances); err != nil {
		return nil, fmt.Errorf("list server instances: %w", err)
	}
	return instances, nil
}
