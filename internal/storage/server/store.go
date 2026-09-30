//go:build server

// Package server 管理服务端 PostgreSQL 连接、迁移执行和证书缓存等存储适配器。
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

const (
	postgresMaxOpenConnections    = 8
	postgresMaxIdleConnections    = 2
	postgresConnectionMaxLifetime = 30 * time.Minute
	postgresConnectionMaxIdleTime = 5 * time.Minute
	postgresConnectTimeout        = time.Minute
	postgresMigrationTimeout      = 10 * time.Minute
	// postgresMaintenanceReadTimeout 是维护连接等待单条语句返回的上限，覆盖大规模索引构建。
	postgresMaintenanceReadTimeout = 6 * time.Hour
	// postgresMaintenanceMaxOpenConnections 是维护连接池的最大连接数。
	postgresMaintenanceMaxOpenConnections = 2
)

// Store 管理业务连接池与执行长时间 DDL 的维护连接池。
type Store struct {
	db          *bun.DB
	maintenance *bun.DB
}

// Open 连接 PostgreSQL 并执行数据库迁移。
func Open(ctx context.Context, config serverconfig.DatabaseConfig) (*Store, error) {
	sqlDB := sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(postgresDSN(config)),
		pgdriver.WithConnParams(map[string]any{"timezone": "UTC"}),
	))
	sqlDB.SetMaxOpenConns(postgresMaxOpenConnections)
	sqlDB.SetMaxIdleConns(postgresMaxIdleConnections)
	sqlDB.SetConnMaxLifetime(postgresConnectionMaxLifetime)
	sqlDB.SetConnMaxIdleTime(postgresConnectionMaxIdleTime)

	db := bun.NewDB(sqlDB, pgdialect.New())
	connectCtx, cancelConnect := context.WithTimeout(ctx, postgresConnectTimeout)
	connectErr := sqlDB.PingContext(connectCtx)
	cancelConnect()
	if connectErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", connectErr)
	}
	slog.Info("PostgreSQL 连接成功",
		"timezone", "UTC",
		"max_open_connections", postgresMaxOpenConnections,
		"max_idle_connections", postgresMaxIdleConnections,
	)

	migrationCtx, cancelMigration := context.WithTimeout(ctx, postgresMigrationTimeout)
	migrationErr := migrate(migrationCtx, sqlDB)
	if migrationErr == nil {
		migrationErr = EnsureMessagePartitions(migrationCtx, db, time.Now())
	}
	cancelMigration()
	if migrationErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate PostgreSQL: %w", migrationErr)
	}

	maintenance := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(postgresDSN(config)),
		pgdriver.WithConnParams(map[string]any{"timezone": "UTC"}),
		pgdriver.WithReadTimeout(postgresMaintenanceReadTimeout),
	)), pgdialect.New())
	maintenance.SetMaxOpenConns(postgresMaintenanceMaxOpenConnections)
	return &Store{db: db, maintenance: maintenance}, nil
}

// postgresDSN 将分项配置编码为 PostgreSQL 驱动连接地址。
func postgresDSN(config serverconfig.DatabaseConfig) string {
	databaseURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(config.User, config.Password),
		Host:   net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		Path:   config.Name,
	}
	query := databaseURL.Query()
	query.Set("sslmode", config.SSLMode)
	databaseURL.RawQuery = query.Encode()
	return databaseURL.String()
}

// Close 关闭业务与维护连接池。
func (s *Store) Close() error {
	return errors.Join(s.db.Close(), s.maintenance.Close())
}

// MaintenanceDB 返回执行索引构建等长时间 DDL 的维护连接池。
func (s *Store) MaintenanceDB() *bun.DB {
	return s.maintenance
}

// DB 返回服务端使用的 Bun 数据库连接。
func (s *Store) DB() *bun.DB {
	return s.db
}
