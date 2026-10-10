//go:build server

package serverinstance

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
	InstanceHeartbeatInterval = 10 * time.Second
	// InstanceOnlineWindow 是判断服务端进程在线的心跳时限，超过时限视为失联。
	InstanceOnlineWindow = 30 * time.Second
	// InstanceFenceWindow 是终止失联服务端进程全部数据库连接的心跳时限。
	InstanceFenceWindow = 2 * time.Minute
	// instanceApplicationPrefix 是服务端进程数据库连接 application_name 的前缀，后接实例编号。
	instanceApplicationPrefix = "server:"
	// instanceRetention 是失联服务端进程记录的保留时长。
	instanceRetention = time.Hour
)

// ServerInstance 定义一个服务端进程及其最近一次心跳：BusDriver 为消息总线的传输驱动，BusConnected 为消息总线是否可用，
// BusMessageRate 与 BusFailures 为上一个心跳间隔内消息总线的发送速率（条/秒）与发送失败和丢弃的消息数，Online 表示心跳在 InstanceOnlineWindow 之内，Config 为进程启动时的服务端配置。
type ServerInstance struct {
	ID             string                   `bun:"id"`
	StartedAt      time.Time                `bun:"started_at"`
	HeartbeatAt    time.Time                `bun:"heartbeat_at"`
	Hostname       string                   `bun:"hostname"`
	Version        string                   `bun:"version"`
	BusDriver      string                   `bun:"bus_driver"`
	BusConnected   bool                     `bun:"bus_connected"`
	BusMessageRate float64                  `bun:"bus_message_rate"`
	BusFailures    int                      `bun:"bus_failures"`
	Online         bool                     `bun:"online"`
	Config         serverconfig.Diagnostics `bun:"config,type:jsonb"`
}

// InstanceReport 定义一次心跳上报的服务端进程信息，BusDriver 与 Config 只在登记时写入。
type InstanceReport struct {
	ID             string
	Hostname       string
	Version        string
	BusDriver      string
	BusConnected   bool
	BusMessageRate float64
	BusFailures    int
	Config         serverconfig.Diagnostics
}

// InstanceApplicationName 返回服务端进程全部数据库连接使用的 application_name，供隔离失联进程时识别其连接。
func InstanceApplicationName(id string) string {
	return instanceApplicationPrefix + id
}

// ReportInstance 登记服务端进程或刷新其心跳，并删除失联超过保留时长且已没有数据库连接的进程记录；仍有连接的记录保留到隔离之后。
func ReportInstance(ctx context.Context, db bun.IDB, report InstanceReport) error {
	if _, err := db.NewInsert().Model(&servermodels.ServerInstance{
		ID: report.ID, Hostname: report.Hostname, Version: report.Version,
		BusDriver: report.BusDriver, BusConnected: report.BusConnected,
		BusMessageRate: report.BusMessageRate, BusFailures: report.BusFailures,
		Config: report.Config,
	}).
		On("CONFLICT (id) DO UPDATE").
		Set("heartbeat_at = now()").
		Set("bus_connected = EXCLUDED.bus_connected").
		Set("bus_message_rate = EXCLUDED.bus_message_rate").
		Set("bus_failures = EXCLUDED.bus_failures").
		Exec(ctx); err != nil {
		return fmt.Errorf("report server instance: %w", err)
	}
	if _, err := db.NewDelete().Model((*servermodels.ServerInstance)(nil)).
		Where("heartbeat_at < now() - make_interval(secs => ?)", instanceRetention.Seconds()).
		Where("NOT EXISTS (SELECT 1 FROM pg_stat_activity AS a WHERE a.application_name = ? || si.id::text)", instanceApplicationPrefix).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete lost server instances: %w", err)
	}
	return nil
}

// FenceLostInstances 终止心跳超过 InstanceFenceWindow 的其他服务端进程的全部数据库连接，返回终止的连接数；
// 失联进程的通知监听、行锁与实例锁随连接释放，进程恢复后因实例锁丢失而退出。
func FenceLostInstances(ctx context.Context, db bun.IDB, selfID string) (int, error) {
	var terminated int
	if err := db.NewRaw(`
		SELECT count(*) FILTER (WHERE pg_terminate_backend(a.pid))
		FROM pg_stat_activity AS a
		JOIN server_instances AS si ON a.application_name = ? || si.id::text
		WHERE si.id::text <> ? AND si.heartbeat_at < now() - make_interval(secs => ?)
	`, instanceApplicationPrefix, selfID, InstanceFenceWindow.Seconds()).Scan(ctx, &terminated); err != nil {
		return 0, fmt.Errorf("fence lost server instances: %w", err)
	}
	return terminated, nil
}

// RemoveInstance 删除正常退出的服务端进程记录。
func RemoveInstance(ctx context.Context, db bun.IDB, id string) error {
	if _, err := db.NewDelete().Model((*servermodels.ServerInstance)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("remove server instance: %w", err)
	}
	return nil
}

// ListInstances 按启动时间返回保留中的服务端进程，在线的排在前面。
func ListInstances(ctx context.Context, db bun.IDB) ([]ServerInstance, error) {
	instances := make([]ServerInstance, 0)
	if err := db.NewSelect().Model((*servermodels.ServerInstance)(nil)).
		ColumnExpr("si.id::text AS id, si.started_at, si.heartbeat_at, si.hostname, si.version, si.bus_driver, si.bus_connected, si.bus_message_rate, si.bus_failures, si.config").
		ColumnExpr("si.heartbeat_at >= now() - make_interval(secs => ?) AS online", InstanceOnlineWindow.Seconds()).
		OrderExpr("online DESC, si.started_at ASC, si.id ASC").
		Scan(ctx, &instances); err != nil {
		return nil, fmt.Errorf("list server instances: %w", err)
	}
	return instances, nil
}

// ListInstanceSummaries 返回保留中的服务端进程，只含编号、启动与心跳时间、主机名、版本、启动配置与在线状态，不读取消息总线状态；部署准入与状态命令在执行迁移前读取部署中的进程时使用。
func ListInstanceSummaries(ctx context.Context, db bun.IDB) ([]ServerInstance, error) {
	instances := make([]ServerInstance, 0)
	if err := db.NewSelect().Model((*servermodels.ServerInstance)(nil)).
		ColumnExpr("si.id::text AS id, si.started_at, si.heartbeat_at, si.hostname, si.version, si.config").
		ColumnExpr("si.heartbeat_at >= now() - make_interval(secs => ?) AS online", InstanceOnlineWindow.Seconds()).
		OrderExpr("online DESC, si.started_at ASC, si.id ASC").
		Scan(ctx, &instances); err != nil {
		return nil, fmt.Errorf("list server instance summaries: %w", err)
	}
	return instances, nil
}

// HTTPSServersOnline 判断部署中是否有心跳在线且直接提供 HTTPS 的服务器。
func HTTPSServersOnline(ctx context.Context, db bun.IDB) (bool, error) {
	online, err := db.NewSelect().Model((*servermodels.ServerInstance)(nil)).
		Where("si.heartbeat_at >= now() - make_interval(secs => ?)", InstanceOnlineWindow.Seconds()).
		Where("coalesce((si.config->>'httpsPort')::int, 0) > 0").
		Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check HTTPS servers: %w", err)
	}
	return online, nil
}

// EmbeddedNATSConflict 判断本进程的任务队列 NATS 模式是否与部署中其他主机上在线的服务端进程冲突：内嵌 NATS 只用于单台服务器，
// embedded 为真时存在其他主机的在线进程即冲突，为假时存在其他主机上使用内嵌 NATS 的在线进程即冲突；同一主机上的记录是重启前的本机进程，不计入。
func EmbeddedNATSConflict(ctx context.Context, db bun.IDB, id, hostname string, embedded bool) (bool, error) {
	query := db.NewSelect().Model((*servermodels.ServerInstance)(nil)).
		Where("si.id <> ? AND si.hostname <> ?", id, hostname).
		Where("si.heartbeat_at >= now() - make_interval(secs => ?)", InstanceOnlineWindow.Seconds())
	if !embedded {
		query = query.Where("coalesce(si.config->'nats'->>'url', '') = ''")
	}
	conflict, err := query.Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check embedded NATS conflict: %w", err)
	}
	return conflict, nil
}
