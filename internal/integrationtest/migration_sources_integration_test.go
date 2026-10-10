//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
)

// TestMigrationSources 验证多个迁移来源合成一个迁移历史：不同来源的版本号重复时拒绝合并，SQL 函数扩展点只能由声明覆盖的一个来源在默认实现之后重新定义，合并后的迁移一起执行与撤销，只有部分来源的程序拒绝校验与撤销含有它没有的迁移的数据库。
func TestMigrationSources(t *testing.T) {
	ctx := context.Background()
	probe := func(name string) fstest.MapFS {
		return fstest.MapFS{name: &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n\n-- +goose Down\nSELECT 1;\n")}}
	}
	_, err := serverstorage.NewMigrations(serverstorage.MigrationSource{Files: serverstorage.CoreMigrationFiles()}, serverstorage.MigrationSource{Files: probe("99990101000000_first.sql")}, serverstorage.MigrationSource{Files: probe("99990101000000_second.sql")})
	require.ErrorContains(t, err, "99990101000000")

	// 共享模型可用性扩展点只能由声明覆盖的一个来源、在核心默认实现之后重新定义，核心不能在第二个文件中改动它。
	core := serverstorage.MigrationSource{Files: serverstorage.CoreMigrationFiles()}
	override := func(name string) fstest.MapFS {
		return fstest.MapFS{name: &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE OR REPLACE FUNCTION shared_model_allowed(workspace_id uuid, model_id uuid) RETURNS boolean LANGUAGE sql AS $$ SELECT true $$;\n\n-- +goose Down\nSELECT 1;\n")}}
	}
	declared := []string{serverstorage.SharedModelAllowedFunction}
	_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: override("99990101000001_override.sql")})
	require.ErrorContains(t, err, "without declaring the override")
	_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: override("20000101000000_override.sql"), Overrides: declared})
	require.ErrorContains(t, err, "before its default")
	_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: override("99990101000001_first.sql"), Overrides: declared}, serverstorage.MigrationSource{Files: override("99990101000002_second.sql"), Overrides: declared})
	require.ErrorContains(t, err, "overridden by 2 migration sources")
	_, err = serverstorage.NewMigrations(serverstorage.MigrationSource{Files: fstest.MapFS{}}, serverstorage.MigrationSource{Files: override("99990101000001_override.sql"), Overrides: declared})
	require.ErrorContains(t, err, "exactly one file")
	_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: override("99990101000001_override.sql"), Overrides: declared})
	require.NoError(t, err)
	// 限定模式、带引号与动态 SQL 的改动同样视为定义；只出现在 Down 段中的不算，声明了覆盖却没有定义时报错。
	migration := func(name, up, down string) fstest.MapFS {
		return fstest.MapFS{name: &fstest.MapFile{Data: []byte("-- +goose Up\n" + up + "\n\n-- +goose Down\n" + down + "\n")}}
	}
	for _, statement := range []string{
		"CREATE OR REPLACE FUNCTION public.shared_model_allowed(workspace_id uuid, model_id uuid) RETURNS boolean LANGUAGE sql AS $$ SELECT true $$;",
		`CREATE OR REPLACE FUNCTION "shared_model_allowed"(workspace_id uuid, model_id uuid) RETURNS boolean LANGUAGE sql AS $$ SELECT true $$;`,
		`DO $$ BEGIN EXECUTE format('CREATE OR REPLACE FUNCTION %I(w uuid, m uuid) RETURNS boolean LANGUAGE sql AS ''SELECT true''', 'SHARED_MODEL_ALLOWED'); END $$;`,
	} {
		_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: migration("99990101000001_override.sql", statement, "SELECT 1;")})
		require.ErrorContains(t, err, "without declaring the override", statement)
	}
	_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: migration("99990101000001_unrelated.sql",
		"SELECT 1;",
		"CREATE OR REPLACE FUNCTION shared_model_allowed(workspace_id uuid, model_id uuid) RETURNS boolean LANGUAGE sql AS $$ SELECT false $$;")})
	require.NoError(t, err)
	// Up 段中的注释、伪装成注释或 Down 指令的字符串都不能藏住改动。
	for _, statement := range []string{
		"-- shared_model_allowed\nSELECT 1;",
		"SELECT '--'; CREATE OR REPLACE FUNCTION shared_model_allowed(workspace_id uuid, model_id uuid) RETURNS boolean LANGUAGE sql AS $$ SELECT true $$;",
		"SELECT '-- +goose Down';\nCREATE OR REPLACE FUNCTION shared_model_allowed(workspace_id uuid, model_id uuid) RETURNS boolean LANGUAGE sql AS $$ SELECT true $$;",
		"-- +GOOSE Down\nCREATE OR REPLACE FUNCTION shared_model_allowed(workspace_id uuid, model_id uuid) RETURNS boolean LANGUAGE sql AS $$ SELECT true $$;",
	} {
		_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: migration("99990101000001_override.sql", statement, "SELECT 1;")})
		require.ErrorContains(t, err, "without declaring the override", statement)
	}
	_, err = serverstorage.NewMigrations(core, serverstorage.MigrationSource{Files: migration("99990101000001_unrelated.sql", "SELECT 1;", "SELECT 1;"), Overrides: declared})
	require.ErrorContains(t, err, "none of its migrations defines it")
	_, err = serverstorage.NewMigrations(serverstorage.MigrationSource{Files: fstest.MapFS{
		"20000101000000_default.sql": override("20000101000000_default.sql")["20000101000000_default.sql"],
		"20000101000001_change.sql":  override("20000101000001_change.sql")["20000101000001_change.sql"],
	}})
	require.ErrorContains(t, err, "exactly one file")

	extra := fstest.MapFS{"99990101000000_create_migration_probe_table.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\nCREATE TABLE migration_probes (id integer PRIMARY KEY);\n\n-- +goose Down\nDROP TABLE migration_probes;\n",
	)}}
	merged, err := serverstorage.NewMigrations(serverstorage.MigrationSource{Files: serverstorage.CoreMigrationFiles()}, serverstorage.MigrationSource{Files: extra})
	require.NoError(t, err)
	coreOnly := serverstorage.CoreMigrations()
	coreVersions, err := coreOnly.Versions()
	require.NoError(t, err)
	mergedVersions, err := merged.Versions()
	require.NoError(t, err)
	require.Equal(t, append(append([]int64{}, coreVersions...), 99990101000000), mergedVersions)

	// 新建独立数据库执行合并后的迁移。
	config := servertest.DatabaseConfig(t)
	base, err := serverstorage.Connect(ctx, config, coreOnly, "", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = base.Close() })
	name := config.Name + "_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16]
	_, err = base.DB().ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	config.Name = name
	store, err := serverstorage.Open(ctx, config, merged)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
		_, _ = base.DB().ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	db := store.DB().DB
	_, err = db.ExecContext(ctx, "SELECT count(*) FROM migration_probes")
	require.NoError(t, err)
	state, err := merged.State(ctx, db)
	require.NoError(t, err)
	require.Equal(t, mergedVersions, state.Applied)
	require.Empty(t, state.Pending)
	require.Error(t, coreOnly.Verify(ctx, db))
	_, err = coreOnly.Rollback(ctx, db, nil)
	require.Error(t, err)
	state, err = merged.State(ctx, db)
	require.NoError(t, err)
	require.Equal(t, mergedVersions, state.Applied)

	rolledBack, err := merged.Rollback(ctx, db, coreVersions)
	require.NoError(t, err)
	require.Equal(t, []int64{99990101000000}, rolledBack)
	require.NoError(t, coreOnly.Verify(ctx, db))
	_, err = db.ExecContext(ctx, "SELECT count(*) FROM migration_probes")
	require.Error(t, err)
}
