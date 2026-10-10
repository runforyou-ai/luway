//go:build server

package integrationtest

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestAdmitInstance 验证服务端进程加入部署：首次加入时迁移后登记版本，低版本拒绝启动，高版本先登记再等其他版本退出后迁移，确认回退时部署中须没有运行的进程，之后以低版本改写，有其他服务器运行时要求部署配置开启对象存储且各服务器不使用自动签发证书，崩溃进程的登记不视为运行中，等其他版本退出时还要求心跳过期，准入锁长时间占用时持续等待。
func TestAdmitInstance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 新建未迁移的空库，首次加入时由准入流程执行迁移。
	config := servertest.DatabaseConfig(t)
	base, err := serverstorage.Connect(ctx, config, serverstorage.CoreMigrations(), "", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = base.Close() })
	name := config.Name + "_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16]
	_, err = base.DB().ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	config.Name = name
	store, err := serverstorage.Connect(ctx, config, serverstorage.CoreMigrations(), "", 0)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
		_, _ = base.DB().ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	db := store.DB()

	var migrations atomic.Int32
	// admit 以指定版本在新主机上加入部署，返回本进程登记信息与实例锁。
	admit := func(version string) (serverinstanceaction.InstanceReport, *serverinstanceaction.InstanceLease, error) {
		report := serverinstanceaction.InstanceReport{
			ID: uuid.NewV7().String(), Hostname: "host-" + random.Hex(4), Version: version,
			Config: serverconfig.Diagnostics{},
		}
		lease, err := serverinstanceaction.AdmitInstance(ctx, db, serverinstanceaction.Admission{
			Instance: report,
			Migrate: func(ctx context.Context) error {
				migrations.Add(1)
				return store.Migrate(ctx)
			},
		})
		return report, lease, err
	}
	// exit 模拟进程正常退出：释放实例锁并删除登记。
	exit := func(report serverinstanceaction.InstanceReport, lease *serverinstanceaction.InstanceLease) {
		t.Helper()
		lease.Release()
		require.NoError(t, serverinstanceaction.RemoveInstance(ctx, db, report.ID))
	}
	requireVersion := func(want string) {
		t.Helper()
		version, err := serverinstanceaction.DeploymentVersion(ctx, db)
		require.NoError(t, err)
		require.Equal(t, want, version)
	}

	// 首次加入：迁移后登记部署版本与本进程，随后完成首次安装并开启对象存储。
	first, firstLease, err := admit("1.0.0")
	require.NoError(t, err)
	require.EqualValues(t, 1, migrations.Load())
	requireVersion("1.0.0")
	_, err = db.NewRaw("INSERT INTO platforms (registration_policy, workspace_creation_policy, public_url, s3_enabled) VALUES ('invitation_only', 'platform_admin', ?, true)", servertest.PublicURL).Exec(ctx)
	require.NoError(t, err)
	setS3 := func(enabled bool) {
		t.Helper()
		_, err := db.NewRaw("UPDATE platforms SET s3_enabled = ?", enabled).Exec(ctx)
		require.NoError(t, err)
	}

	// 低版本拒绝启动，不执行迁移。
	_, _, err = admit("0.9.0")
	require.ErrorIs(t, err, serverinstanceaction.ErrVersionOutdated)

	// 有其他服务器运行时，部署配置未开启对象存储时拒绝启动，不执行迁移。
	setS3(false)
	_, _, err = admit("1.0.0")
	require.ErrorIs(t, err, serverinstanceaction.ErrMultiServerUnsupported)
	setS3(true)
	require.EqualValues(t, 1, migrations.Load())

	// 进程崩溃后登记仍在心跳时限内，但实例锁已释放，不再视为运行中。
	firstLease.Release()
	setS3(false)
	solo, soloLease, err := admit("1.0.0")
	setS3(true)
	require.NoError(t, err)
	exit(solo, soloLease)

	// 高版本先登记部署版本，等运行中的旧版本进程全部退出后再迁移。
	second, secondLease, err := admit("1.0.0")
	require.NoError(t, err)
	third, thirdLease, err := admit("1.0.0")
	require.NoError(t, err)
	require.EqualValues(t, 4, migrations.Load())
	type admission struct {
		report serverinstanceaction.InstanceReport
		lease  *serverinstanceaction.InstanceLease
		err    error
	}
	done := make(chan admission, 1)
	go func() {
		report, lease, err := admit("1.1.0")
		done <- admission{report, lease, err}
	}()
	require.Eventually(t, func() bool {
		version, err := serverinstanceaction.DeploymentVersion(ctx, db)
		return err == nil && version == "1.1.0"
	}, 5*time.Second, 50*time.Millisecond)
	time.Sleep(1500 * time.Millisecond)
	require.Empty(t, done)
	require.EqualValues(t, 4, migrations.Load())
	exit(second, secondLease)
	// 实例锁已释放但心跳仍在在线时限内的旧版本进程可能仍在服务，心跳过期后才视为退出；先前崩溃的进程同样等心跳过期。
	thirdLease.Release()
	time.Sleep(1500 * time.Millisecond)
	require.Empty(t, done)
	_, err = db.NewUpdate().Table("server_instances").Set("heartbeat_at = now() - interval '1 minute'").Where("id IN (?)", bun.List([]string{first.ID, third.ID})).Exec(ctx)
	require.NoError(t, err)
	var upgraded admission
	select {
	case upgraded = <-done:
		require.NoError(t, upgraded.err)
	case <-time.After(5 * time.Second):
		t.Fatal("高版本进程未在旧版本退出后完成加入")
	}
	require.EqualValues(t, 5, migrations.Load())

	// 检查低版本能否加入时报告版本过低且不改写部署版本；确认回退时部署中有运行的进程（含心跳过期但仍持有实例锁的进程）则拒绝，全部退出后改写为低版本，低版本进程随即加入。
	deployed, err := serverinstanceaction.CheckAdmission(ctx, db, "1.0.0", false)
	require.ErrorIs(t, err, serverinstanceaction.ErrVersionOutdated)
	require.Equal(t, "1.1.0", deployed)
	deployed, err = serverinstanceaction.CheckAdmission(ctx, db, "1.2.0", false)
	require.NoError(t, err)
	require.Equal(t, "1.1.0", deployed)
	requireVersion("1.1.0")
	_, err = serverinstanceaction.CheckAdmission(ctx, db, "1.0.0", true)
	require.ErrorIs(t, err, serverinstanceaction.ErrServersRunning)
	_, err = db.NewUpdate().Table("server_instances").Set("heartbeat_at = now() - interval '1 minute'").Where("id = ?", upgraded.report.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = serverinstanceaction.CheckAdmission(ctx, db, "1.0.0", true)
	require.ErrorIs(t, err, serverinstanceaction.ErrServersRunning, "心跳过期但仍持有实例锁的进程视为运行中")
	requireVersion("1.1.0")
	exit(upgraded.report, upgraded.lease)
	_, err = serverinstanceaction.CheckAdmission(ctx, db, "1.0.0", true)
	require.NoError(t, err)
	requireVersion("1.0.0")
	downgraded, downgradedLease, err := admit("1.0.0")
	require.NoError(t, err)
	exit(downgraded, downgradedLease)
	requireVersion("1.0.0")

	// 准入锁被占用超过连接读超时后释放，等待中的进程仍能加入。
	holder, err := db.Conn(ctx)
	require.NoError(t, err)
	defer holder.Close()
	err = serverstorage.SessionLock(ctx, holder, serverstorage.LockDeploymentAdmission)
	require.NoError(t, err)
	go func() {
		report, lease, err := admit("1.0.0")
		done <- admission{report, lease, err}
	}()
	time.Sleep(12 * time.Second)
	require.Empty(t, done)
	err = serverstorage.SessionUnlock(ctx, holder, serverstorage.LockDeploymentAdmission)
	require.NoError(t, err)
	select {
	case waited := <-done:
		require.NoError(t, waited.err)
		exit(waited.report, waited.lease)
	case <-time.After(5 * time.Second):
		t.Fatal("准入锁释放后进程未完成加入")
	}
}

// TestAdmitInstanceBeforeMigration 验证部署版本表与实例表缺少后续迁移新增的列时，新版本进程仍能在迁移前通过准入检查、登记版本并完成迁移。
func TestAdmitInstanceBeforeMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	_, err := db.NewInsert().Model(&servermodels.DeploymentVersion{Version: "1.0.0"}).Column("version").Exec(ctx)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "ALTER TABLE deployment_versions DROP COLUMN created_at; ALTER TABLE server_instances DROP COLUMN created_at")
	require.NoError(t, err)
	deployed, err := serverinstanceaction.CheckAdmission(ctx, db, "1.1.0", false)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", deployed)

	lease, err := serverinstanceaction.AdmitInstance(ctx, db, serverinstanceaction.Admission{
		Instance: serverinstanceaction.InstanceReport{ID: uuid.NewV7().String(), Hostname: "host-before-migration", Version: "1.1.0", Config: serverconfig.Diagnostics{}},
		Migrate: func(ctx context.Context) error {
			_, err := db.ExecContext(ctx, `ALTER TABLE deployment_versions ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
				ALTER TABLE server_instances ADD COLUMN created_at timestamptz NOT NULL DEFAULT now()`)
			return err
		},
	})
	require.NoError(t, err)
	lease.Release()
	version, err := serverinstanceaction.DeploymentVersion(ctx, db)
	require.NoError(t, err)
	require.Equal(t, "1.1.0", version)
}
