//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
)

// TestRollbackMigrations 验证撤销迁移只撤销不在保留列表中的迁移，全部迁移的 Down 都能执行，撤销后可重新迁移到最新结构。
func TestRollbackMigrations(t *testing.T) {
	// 全量撤销与重建迁移负载较重，不与其他集成测试并行。
	ctx := context.Background()
	// 新建并迁移一个独立数据库，撤销迁移不影响其他测试。
	config := servertest.DatabaseConfig(t)
	base, err := serverstorage.Connect(ctx, config, serverstorage.CoreMigrations(), "", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = base.Close() })
	name := config.Name + "_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16]
	_, err = base.DB().ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	config.Name = name
	store, err := serverstorage.Open(ctx, config, serverstorage.CoreMigrations())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
		_, _ = base.DB().ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	db := store.DB().DB
	versions, err := serverstorage.CoreMigrations().Versions()
	require.NoError(t, err)
	require.NotEmpty(t, versions)

	keep := versions[:len(versions)-3]
	rolledBack, err := serverstorage.CoreMigrations().Rollback(ctx, db, keep)
	require.NoError(t, err)
	require.Equal(t, []int64{versions[len(versions)-1], versions[len(versions)-2], versions[len(versions)-3]}, rolledBack)
	state, err := serverstorage.CoreMigrations().State(ctx, db)
	require.NoError(t, err)
	require.Equal(t, keep, state.Applied)
	require.Equal(t, versions[len(versions)-3:], state.Pending)

	rolledBack, err = serverstorage.CoreMigrations().Rollback(ctx, db, nil)
	require.NoError(t, err)
	require.Len(t, rolledBack, len(keep))
	state, err = serverstorage.CoreMigrations().State(ctx, db)
	require.NoError(t, err)
	require.Empty(t, state.Applied)

	require.NoError(t, store.Migrate(ctx))
	state, err = serverstorage.CoreMigrations().State(ctx, db)
	require.NoError(t, err)
	require.Equal(t, versions, state.Applied)
	require.Empty(t, state.Pending)
}
