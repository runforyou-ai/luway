//go:build server

// Package serverinstance 实现服务端进程加入部署的准入、实例锁、心跳登记、失联进程隔离与部署版本登记，服务端启动与服务器命令行共用。
package serverinstance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

const (
	// admissionTimeout 是等待部署准入锁与其他版本进程退出的总时限。
	admissionTimeout = 10 * time.Minute
	// admissionPollInterval 是尝试取得准入锁与等待其他版本进程退出时的检查间隔。
	admissionPollInterval = time.Second
)

var (
	// ErrVersionOutdated 表示本进程版本低于部署已登记的版本。
	ErrVersionOutdated = errors.New("server version is older than the deployment version")
	// ErrMultiServerUnsupported 表示已有其他服务端进程在线，而部署配置只支持单台服务器。
	ErrMultiServerUnsupported = errors.New("deployment configuration supports a single server only")
	// ErrInstanceLeaseLost 表示持有实例锁的数据库连接已断开，其他服务端进程可能已把本进程视为已退出。
	ErrInstanceLeaseLost = errors.New("server instance lock connection lost")
	// ErrServersRunning 表示部署中仍有心跳在线或持有实例锁的服务端进程。
	ErrServersRunning = errors.New("servers are still running in the deployment")
)

// Admission 定义服务端进程加入部署的参数：Instance 为本进程的登记信息，Migrate 执行数据库迁移。
type Admission struct {
	Instance InstanceReport
	Migrate  func(context.Context) error
}

// AdmitInstance 在部署准入锁内让服务端进程加入部署：登记本进程版本并等待版本不同的运行中进程退出，校验多台服务器的部署约束，执行数据库迁移，取得实例锁后登记本进程；返回的实例锁在进程退出时释放。
func AdmitInstance(ctx context.Context, db *bun.DB, admission Admission) (*InstanceLease, error) {
	ctx, cancel := context.WithTimeout(ctx, admissionTimeout)
	defer cancel()
	conn, release, err := lockAdmission(ctx, db)
	if err != nil {
		return nil, err
	}
	defer release()

	// 部署版本表在首次迁移后才存在；已存在时先登记版本、等其他版本进程退出并校验部署约束，迁移只在同一版本下执行。
	var deployed bool
	if err := conn.NewRaw("SELECT to_regclass('deployment_versions') IS NOT NULL").Scan(ctx, &deployed); err != nil {
		return nil, fmt.Errorf("check deployment version table: %w", err)
	}
	if deployed {
		if err := registerVersion(ctx, conn, admission.Instance.Version, false); err != nil {
			return nil, err
		}
		if err := waitForOtherVersions(ctx, conn, admission.Instance.Version); err != nil {
			return nil, err
		}
		if err := checkMultiServer(ctx, conn); err != nil {
			return nil, err
		}
	}
	if err := admission.Migrate(ctx); err != nil {
		return nil, err
	}
	if !deployed {
		if err := registerVersion(ctx, conn, admission.Instance.Version, false); err != nil {
			return nil, err
		}
	}
	lease, err := holdInstance(ctx, db, admission.Instance.ID)
	if err != nil {
		return nil, err
	}
	if err := ReportInstance(ctx, conn, admission.Instance); err != nil {
		lease.Release()
		return nil, err
	}
	return lease, nil
}

// CheckAdmission 检查 version 版本的服务端能否加入部署，返回部署已登记的版本，尚未登记时为空：部署版本更高时返回 ErrVersionOutdated；acceptDowngrade 为真时在部署准入锁内确认部署中没有运行的服务端进程后把部署版本改为 version，有运行的进程时返回 ErrServersRunning。
func CheckAdmission(ctx context.Context, db *bun.DB, version string, acceptDowngrade bool) (string, error) {
	var deployed bool
	if err := db.NewRaw("SELECT to_regclass('deployment_versions') IS NOT NULL").Scan(ctx, &deployed); err != nil {
		return "", fmt.Errorf("check deployment version table: %w", err)
	}
	if !deployed {
		return "", nil
	}
	var current []servermodels.DeploymentVersion
	if err := db.NewSelect().Model(&current).Column("version").Scan(ctx); err != nil {
		return "", fmt.Errorf("load deployment version: %w", err)
	}
	if len(current) == 0 {
		return "", nil
	}
	deployment := current[0].Version
	if buildinfo.CompareVersions(version, deployment) >= 0 {
		return deployment, nil
	}
	if !acceptDowngrade {
		return deployment, fmt.Errorf("%w: server %s, deployment %s", ErrVersionOutdated, version, deployment)
	}
	return deployment, WithStoppedDeployment(ctx, db, func(ctx context.Context, conn bun.Conn) error {
		return registerVersion(ctx, conn, version, true)
	})
}

// WithStoppedDeployment 在部署准入锁内确认部署中没有心跳在线或持有实例锁的服务端进程后执行 action，有运行的进程时返回 ErrServersRunning；action 结束前其他服务端进程不能加入部署。
func WithStoppedDeployment(ctx context.Context, db *bun.DB, action func(ctx context.Context, conn bun.Conn) error) error {
	lockCtx, cancel := context.WithTimeout(ctx, admissionTimeout)
	defer cancel()
	conn, release, err := lockAdmission(lockCtx, db)
	if err != nil {
		return err
	}
	defer release()
	live, err := liveInstances(ctx, conn)
	if err != nil {
		return err
	}
	if len(live) > 0 {
		names := arr.Map(live, func(instance ServerInstance) string { return instance.Hostname + " " + instance.Version })
		return fmt.Errorf("%w: %s", ErrServersRunning, strings.Join(names, ", "))
	}
	return action(ctx, conn)
}

// WithAdmissionLock 在部署准入锁内读取仍持有实例锁的服务端进程并执行 action；action 结束前其他服务端进程不能加入部署。
func WithAdmissionLock(ctx context.Context, db *bun.DB, action func(ctx context.Context, running []ServerInstance) error) error {
	conn, release, err := lockAdmission(ctx, db)
	if err != nil {
		return err
	}
	defer release()
	running, err := runningInstances(ctx, conn)
	if err != nil {
		return err
	}
	return action(ctx, running)
}

// lockAdmission 在新的专用连接上每秒尝试一次取得部署准入锁，直到取得锁或 ctx 结束；返回的 release 释放锁并关闭连接。
func lockAdmission(ctx context.Context, db *bun.DB) (bun.Conn, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return bun.Conn{}, nil, fmt.Errorf("open admission connection: %w", err)
	}
	for {
		locked, err := serverstorage.TrySessionLock(ctx, conn, serverstorage.LockDeploymentAdmission, "")
		if err != nil {
			_ = conn.Close()
			return bun.Conn{}, nil, err
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return bun.Conn{}, nil, fmt.Errorf("lock deployment admission: %w", ctx.Err())
		case <-time.After(admissionPollInterval):
		}
	}
	release := func() {
		_ = serverstorage.SessionUnlock(ctx, conn, serverstorage.LockDeploymentAdmission)
		_ = conn.Close()
	}
	return conn, release, nil
}

// DeploymentVersion 返回部署已登记的服务端版本。
func DeploymentVersion(ctx context.Context, db bun.IDB) (string, error) {
	var version string
	if err := db.NewSelect().Model((*servermodels.DeploymentVersion)(nil)).Column("version").Scan(ctx, &version); err != nil {
		return "", fmt.Errorf("load deployment version: %w", err)
	}
	return version, nil
}

// registerVersion 把部署版本登记为 version：部署尚无版本或版本较低时改写，版本相同时保持，版本较高时只在允许回退时改写；登记在迁移前执行，只读写 version 列。
func registerVersion(ctx context.Context, db bun.IDB, version string, acceptDowngrade bool) error {
	var current []servermodels.DeploymentVersion
	if err := db.NewSelect().Model(&current).Column("version").Scan(ctx); err != nil {
		return fmt.Errorf("load deployment version: %w", err)
	}
	if len(current) > 0 {
		comparison := buildinfo.CompareVersions(version, current[0].Version)
		if comparison == 0 {
			return nil
		}
		if comparison < 0 && !acceptDowngrade {
			return fmt.Errorf("%w: server %s, deployment %s", ErrVersionOutdated, version, current[0].Version)
		}
	}
	if _, err := db.NewInsert().Model(&servermodels.DeploymentVersion{Version: version}).Column("version").
		On("CONFLICT ((true)) DO UPDATE").
		Set("version = EXCLUDED.version").
		Exec(ctx); err != nil {
		return fmt.Errorf("register deployment version: %w", err)
	}
	slog.InfoContext(ctx, "已登记部署版本", "version", version)
	return nil
}

// waitForOtherVersions 等待版本与 version 不同的服务端进程全部退出：实例锁已释放且心跳超过在线时限，或登记已删除。
func waitForOtherVersions(ctx context.Context, conn bun.Conn, version string) error {
	ticker := time.NewTicker(admissionPollInterval)
	defer ticker.Stop()
	logged := false
	for {
		live, err := liveInstances(ctx, conn)
		if err != nil {
			return err
		}
		others := arr.FilterMap(live, func(instance ServerInstance) (string, bool) {
			return instance.Hostname + " " + instance.Version, buildinfo.CompareVersions(instance.Version, version) != 0
		})
		if len(others) == 0 {
			return nil
		}
		if !logged {
			slog.InfoContext(ctx, "等待其他版本的服务端进程退出", "instances", strings.Join(others, ", "))
			logged = true
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for servers of other versions to exit (%s): %w", strings.Join(others, ", "), ctx.Err())
		case <-ticker.C:
		}
	}
}

// checkMultiServer 在有其他服务端进程运行时校验部署约束：部署配置开启了对象存储。
func checkMultiServer(ctx context.Context, conn bun.Conn) error {
	instances, err := runningInstances(ctx, conn)
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}
	var s3Enabled []bool
	if err := conn.NewSelect().Model((*servermodels.Platform)(nil)).Column("s3_enabled").Limit(1).Scan(ctx, &s3Enabled); err != nil {
		return fmt.Errorf("load deployment storage: %w", err)
	}
	if len(s3Enabled) == 0 || !s3Enabled[0] {
		return fmt.Errorf("%w: files are stored on local disk, multiple servers require object storage in platform settings", ErrMultiServerUnsupported)
	}
	return nil
}

// liveInstances 返回心跳在线或仍持有实例锁的服务端进程记录。
func liveInstances(ctx context.Context, conn bun.Conn) ([]ServerInstance, error) {
	running, err := runningInstances(ctx, conn)
	if err != nil {
		return nil, err
	}
	instances, err := ListInstanceSummaries(ctx, conn)
	if err != nil {
		return nil, err
	}
	return arr.Filter(instances, func(instance ServerInstance) bool {
		return instance.Online || slices.ContainsFunc(running, func(other ServerInstance) bool { return other.ID == instance.ID })
	}), nil
}

// runningInstances 返回仍持有实例锁的服务端进程记录；在 conn 上能取得实例锁的记录属于已退出的进程，取得后立即释放。
func runningInstances(ctx context.Context, conn bun.Conn) ([]ServerInstance, error) {
	instances, err := ListInstanceSummaries(ctx, conn)
	if err != nil {
		return nil, err
	}
	running := make([]ServerInstance, 0, len(instances))
	for _, instance := range instances {
		exited, err := serverstorage.TrySessionLock(ctx, conn, serverstorage.LockServerInstance, instance.ID)
		if err != nil {
			return nil, err
		}
		if !exited {
			running = append(running, instance)
			continue
		}
		if err := serverstorage.SessionUnlock(ctx, conn, serverstorage.LockServerInstance, instance.ID); err != nil {
			return nil, err
		}
	}
	return running, nil
}

// InstanceLease 是服务端进程运行期间在专用数据库连接上持有的实例锁，连接断开时由 PostgreSQL 释放；Check 与 Release 由同一调用方串行调用。
type InstanceLease struct {
	id   string
	conn bun.Conn
}

// holdInstance 在新的专用连接上取得服务端进程的实例锁。
func holdInstance(ctx context.Context, db *bun.DB, id string) (*InstanceLease, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open server instance connection: %w", err)
	}
	if err := serverstorage.SessionLock(ctx, conn, serverstorage.LockServerInstance, id); err != nil {
		serverstorage.DiscardConn(conn)
		_ = conn.Close()
		return nil, err
	}
	return &InstanceLease{id: id, conn: conn}, nil
}

// Check 确认实例锁所在的连接仍然可用，连接断开时返回 ErrInstanceLeaseLost。
func (l *InstanceLease) Check(ctx context.Context) error {
	if _, err := l.conn.ExecContext(ctx, "SELECT 1"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %w", ErrInstanceLeaseLost, err)
	}
	return nil
}

// Release 释放实例锁并归还连接，释放失败时丢弃连接。
func (l *InstanceLease) Release() {
	_ = serverstorage.SessionUnlock(context.Background(), l.conn, serverstorage.LockServerInstance, l.id)
	_ = l.conn.Close()
}
