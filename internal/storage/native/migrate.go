//go:build !server

package native

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/runforyou-ai/cervi/pkg/goosecheck"
)

// migrate 使用 files 中 migrations 目录下的迁移文件将 SQLite 升级到最新结构。
func migrate(ctx context.Context, db *sql.DB, files fs.FS, platform string) error {
	migrations, err := fs.Sub(files, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	// 已执行版本缺少迁移源文件时拒绝打开，本地数据库需要重建。
	if err := goosecheck.VerifyApplied(ctx, db, goose.DialectSQLite3, provider); err != nil {
		return err
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return nil
	}
	slog.Info(platform+"数据库迁移完成", "applied", len(results))
	return nil
}
