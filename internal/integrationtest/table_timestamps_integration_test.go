//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
)

// TestTableTimestamps 验证每张业务表都有 created_at 与 updated_at 并挂载 set_updated_at 触发器，更新行时写入事务时刻，语句显式写入的值同样被替换。
func TestTableTimestamps(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	ctx := context.Background()

	// 列出缺少非空且带默认值的 timestamptz 类型 created_at、updated_at 列或缺少触发器的表，分区由父表的触发器覆盖。
	var missing []string
	require.NoError(t, db.NewRaw(`
		SELECT c.relname
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND c.relname <> 'goose_db_version'
			AND (
				(SELECT count(*) FROM pg_attribute AS a
					WHERE a.attrelid = c.oid AND a.attname IN ('created_at', 'updated_at') AND NOT a.attisdropped
						AND a.atttypid = 'timestamptz'::regtype AND a.attnotnull AND a.atthasdef) < 2
				OR NOT EXISTS (
					SELECT 1 FROM pg_trigger AS tg
					WHERE tg.tgrelid = c.oid AND tg.tgfoid = 'set_updated_at()'::regprocedure AND NOT tg.tgisinternal AND tg.tgenabled <> 'D'
				)
			)
		ORDER BY c.relname`).Scan(ctx, &missing))
	require.Empty(t, missing)

	preset := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := db.NewRaw("INSERT INTO rate_limits (key, tat, updated_at) VALUES ('updated-at-trigger', now(), ?)", preset).Exec(ctx)
	require.NoError(t, err)
	// 读取测试行的更新时间。
	updatedAt := func() time.Time {
		t.Helper()
		var value time.Time
		require.NoError(t, db.NewRaw("SELECT updated_at FROM rate_limits WHERE key = 'updated-at-trigger'").Scan(ctx, &value))
		return value
	}

	var now time.Time
	require.NoError(t, db.NewRaw("UPDATE rate_limits SET tat = tat + interval '1 second' WHERE key = 'updated-at-trigger' RETURNING now()").Scan(ctx, &now))
	require.True(t, updatedAt().Equal(now), "updated_at = %s, want %s", updatedAt(), now)

	require.NoError(t, db.NewRaw("UPDATE rate_limits SET updated_at = ? WHERE key = 'updated-at-trigger' RETURNING now()", preset).Scan(ctx, &now))
	require.True(t, updatedAt().Equal(now), "updated_at = %s, want %s", updatedAt(), now)
}
