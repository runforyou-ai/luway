// Package goosecheck 校验数据库中已执行的 Goose 迁移版本都有对应的迁移源文件。
package goosecheck

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
)

// VerifyApplied 在版本表存在时校验每个已执行版本都能在 provider 的迁移源中找到，缺失时返回列出这些版本的错误。
func VerifyApplied(ctx context.Context, db *sql.DB, dialect database.Dialect, provider *goose.Provider) error {
	store, err := database.NewStore(dialect, goose.DefaultTablename)
	if err != nil {
		return fmt.Errorf("create migration version store: %w", err)
	}
	// 方言不支持直接查询表是否存在时，以能否读取 0 号版本记录判断版本表是否存在。
	exists, err := store.(database.StoreExtender).TableExists(ctx, db)
	if errors.Is(err, errors.ErrUnsupported) {
		_, getErr := store.GetMigration(ctx, db, 0)
		exists, err = getErr == nil, nil
	}
	if err != nil {
		return fmt.Errorf("check migration version table: %w", err)
	}
	if !exists {
		return nil
	}
	applied, err := store.ListMigrations(ctx, db)
	if err != nil {
		return fmt.Errorf("list applied migrations: %w", err)
	}
	known := set.CollectBy(provider.ListSources(), func(source *goose.Source) int64 { return source.Version })
	missing := arr.FilterMap(applied, func(migration *database.ListMigrationsResult) (int64, bool) {
		return migration.Version, migration.IsApplied && migration.Version > 0 && !known.Has(migration.Version)
	})
	if len(missing) > 0 {
		return fmt.Errorf("database has applied migrations without source files %v; rebuild the database", missing)
	}
	return nil
}
