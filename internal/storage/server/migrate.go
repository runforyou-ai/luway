//go:build server

package server

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/runforyou-ai/luway/pkg/goosecheck"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrate 检查待执行迁移，并在 Goose 迁移锁内执行 PostgreSQL 数据库迁移。
func migrate(ctx context.Context, db *sql.DB) error {
	// 抢锁失败时每秒重试一次，最长等待 5 分钟。
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := newMigrationProvider(db, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}

	// 已执行版本缺少迁移源文件时拒绝启动，数据库需要重建。
	if err := goosecheck.VerifyApplied(ctx, db, goose.DialectPostgres, provider); err != nil {
		return err
	}

	// 无锁检查到迁移已全部应用时直接返回；版本表尚未建立时检查报错，由加锁的 Up 建表并迁移。
	if pending, err := provider.HasPending(ctx); err == nil && !pending {
		return nil
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return nil
	}
	slog.Info("PostgreSQL 数据库迁移完成", "applied", len(results))
	return nil
}

// VerifyMigrationSources 校验数据库中已执行的迁移版本都有内嵌的迁移源文件。
func VerifyMigrationSources(ctx context.Context, db *sql.DB) error {
	provider, err := newMigrationProvider(db)
	if err != nil {
		return err
	}
	return goosecheck.VerifyApplied(ctx, db, goose.DialectPostgres, provider)
}

// MigrationVersion 返回数据库中已执行的最新迁移版本。
func MigrationVersion(ctx context.Context, db *sql.DB) (int64, error) {
	provider, err := newMigrationProvider(db)
	if err != nil {
		return 0, err
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	return version, nil
}

// newMigrationProvider 创建使用内嵌迁移文件、允许乱序执行的 PostgreSQL 迁移器。
func newMigrationProvider(db *sql.DB, options ...goose.ProviderOption) (*goose.Provider, error) {
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, append(options, goose.WithAllowOutofOrder(true))...)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}
