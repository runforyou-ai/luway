//go:build !server

// Package native 管理桌面端与移动端共用的 SQLite 连接、迁移执行、登录凭据与服务器地址，各端迁移文件由调用方传入。
package native

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
)

const sqliteDriverName = "sqlite3"

// Store 管理原生端的 Bun 数据库连接。
type Store struct {
	db *bun.DB
}

// Open 创建 SQLite 数据库并执行 migrations 中 migrations 目录下的迁移；platform 是日志中的端名称，如「桌面端」。
func Open(ctx context.Context, databasePath string, migrations fs.FS, platform string) (*Store, error) {
	db, err := openSQLite(ctx, databasePath)
	if err != nil {
		return nil, fmt.Errorf("initialize SQLite: %w", err)
	}
	slog.Info(platform + " SQLite 连接成功")

	if err := migrate(ctx, db.DB, migrations, platform); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate SQLite: %w", err)
	}

	return &Store{db: db}, nil
}

// DB 返回 Bun 数据库连接，供各端读写专有数据。
func (s *Store) DB() *bun.DB {
	return s.db
}

// Close 关闭 SQLite 数据库连接。
func (s *Store) Close() error {
	return s.db.Close()
}

// openSQLite 以 Unix 权限 0700 创建数据目录、0600 创建数据库文件，再打开 SQLite 连接；Windows 沿用数据目录的访问控制。
func openSQLite(ctx context.Context, databasePath string) (*bun.DB, error) {
	directory := filepath.Dir(databasePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create SQLite data directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect SQLite data directory: %w", err)
	}
	// 预先以 0600 创建数据库文件，SQLite 创建的 WAL 与共享内存文件沿用数据库文件权限。
	file, err := os.OpenFile(databasePath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create SQLite database file: %w", err)
	}
	_ = file.Close()
	// 已存在的数据库、WAL 与共享内存文件统一设为 0600。
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(databasePath+suffix, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("protect SQLite database file: %w", err)
		}
	}

	// 拼接带连接参数的 SQLite 数据源地址。
	query := url.Values{
		"_busy_timeout": {"5000"},
		"_foreign_keys": {"on"},
		"_journal_mode": {"WAL"},
		"mode":          {"rwc"},
	}
	sqlDB, err := sql.Open(sqliteDriverName, databasePath+"?"+query.Encode())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}

	db := bun.NewDB(sqlDB, sqlitedialect.New())
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to SQLite: %w", err)
	}
	return db, nil
}
