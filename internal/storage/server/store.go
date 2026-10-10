//go:build server

// Package server 管理服务端 PostgreSQL 连接、迁移执行、写事务与提交后回调、消息分区与证书签发质询等存储适配器。
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// PostgreSQL 连接池参数与连接、迁移时限。
const (
	// postgresRequestConnections 是业务连接池在任务 Worker 之外为 HTTP 请求与后台循环预留的连接数。
	postgresRequestConnections = 8
	// postgresIdleRequestConnections 是业务连接池在任务 Worker 之外保留的空闲连接数。
	postgresIdleRequestConnections = 2
	// postgresLeaseMaxOpenConnections 是租约续期连接池的最大连接数，容纳路由租约续期与过期清理同时进行。
	postgresLeaseMaxOpenConnections = 3
	postgresConnectionMaxLifetime   = 30 * time.Minute
	postgresConnectionMaxIdleTime   = 5 * time.Minute
	postgresConnectTimeout          = time.Minute
	postgresMigrationTimeout        = 10 * time.Minute
	// postgresIdleTransactionTimeout 是事务内两条语句之间的最长空闲时间，超过后数据库终止该连接。
	postgresIdleTransactionTimeout = "5min"
	// postgresCancelDeadline 是上下文取消后等待数据库结束语句的时限，超时后断开连接。
	postgresCancelDeadline = 5 * time.Second
)

// Store 管理业务连接池、租约续期连接池、本进程的 PostgreSQL 连接配置与程序的数据库迁移。
type Store struct {
	config     *pgx.ConnConfig
	db         *bun.DB
	lease      *bun.DB
	migrations Migrations
}

// Open 连接 PostgreSQL 并执行数据库迁移。
func Open(ctx context.Context, config serverconfig.DatabaseConfig, migrations Migrations) (*Store, error) {
	store, err := Connect(ctx, config, migrations, "", 0)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// Connect 以指定 application_name 连接 PostgreSQL，按本进程的任务 Worker 总数确定业务连接池上限，不执行数据库迁移。
func Connect(ctx context.Context, config serverconfig.DatabaseConfig, migrations Migrations, applicationName string, taskWorkers int) (*Store, error) {
	connConfig, err := ConnConfig(config, applicationName)
	if err != nil {
		return nil, err
	}
	db := openDB(connConfig)
	maxOpen, maxIdle := businessPoolLimits(taskWorkers)
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(postgresConnectionMaxLifetime)
	db.SetConnMaxIdleTime(postgresConnectionMaxIdleTime)

	connectCtx, cancelConnect := context.WithTimeout(ctx, postgresConnectTimeout)
	connectErr := db.PingContext(connectCtx)
	cancelConnect()
	if connectErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", connectErr)
	}

	lease := openDB(connConfig)
	lease.SetMaxOpenConns(postgresLeaseMaxOpenConnections)
	lease.SetMaxIdleConns(postgresLeaseMaxOpenConnections)
	lease.SetConnMaxLifetime(postgresConnectionMaxLifetime)
	lease.SetConnMaxIdleTime(postgresConnectionMaxIdleTime)

	slog.InfoContext(ctx, "PostgreSQL 连接成功",
		"timezone", "UTC",
		"max_open_connections", maxOpen,
		"max_idle_connections", maxIdle,
		"lease_max_open_connections", postgresLeaseMaxOpenConnections,
	)
	return &Store{config: connConfig, db: db, lease: lease, migrations: migrations}, nil
}

// openDB 按驱动配置创建 Bun 连接池，连接把 timestamptz 读取为 UTC 时间。
func openDB(connConfig *pgx.ConnConfig) *bun.DB {
	sqlDB := stdlib.OpenDB(*connConfig, stdlib.OptionAfterConnect(func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC}})
		return nil
	}))
	return bun.NewDB(sqlDB, pgdialect.New())
}

// businessPoolLimits 按任务 Worker 总数返回业务连接池的最大连接数与最大空闲连接数。
func businessPoolLimits(taskWorkers int) (maxOpen, maxIdle int) {
	return taskWorkers + postgresRequestConnections, taskWorkers + postgresIdleRequestConnections
}

// Migrate 执行数据库迁移并创建当前及之后若干月份的消息分区。
func (s *Store) Migrate(ctx context.Context) error {
	migrationCtx, cancel := context.WithTimeout(ctx, postgresMigrationTimeout)
	defer cancel()
	if err := s.migrations.up(migrationCtx, s.db.DB); err != nil {
		return fmt.Errorf("migrate PostgreSQL: %w", err)
	}
	now, err := Now(migrationCtx, s.db)
	if err != nil {
		return fmt.Errorf("migrate PostgreSQL: %w", err)
	}
	if err := EnsureMessagePartitions(migrationCtx, s.db, now); err != nil {
		return fmt.Errorf("migrate PostgreSQL: %w", err)
	}
	return nil
}

// ConnConfig 按分项配置生成以指定 application_name 连接 PostgreSQL 的驱动配置：会话时区为 UTC，事务内空闲超过 postgresIdleTransactionTimeout 时由数据库终止连接，上下文取消时向数据库发送取消请求，语句以简单查询协议执行。
func ConnConfig(config serverconfig.DatabaseConfig, applicationName string) (*pgx.ConnConfig, error) {
	connConfig, err := pgx.ParseConfig(postgresDSN(config))
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL config: %w", err)
	}
	connConfig.RuntimeParams["application_name"] = applicationName
	connConfig.RuntimeParams["timezone"] = "UTC"
	connConfig.RuntimeParams["idle_in_transaction_session_timeout"] = postgresIdleTransactionTimeout
	connConfig.BuildContextWatcherHandler = func(conn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: conn, DeadlineDelay: postgresCancelDeadline}
	}
	// Bun 把参数写入语句文本，每条语句文本各不相同，按简单查询协议执行。
	connConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return connConfig, nil
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

// Close 关闭业务与租约续期连接池。
func (s *Store) Close() error {
	return errors.Join(s.db.Close(), s.lease.Close())
}

// ConnConfig 返回本进程连接 PostgreSQL 的驱动配置副本，供 LISTEN 等独占连接使用。
func (s *Store) ConnConfig() *pgx.ConnConfig {
	return s.config.Copy()
}

// LeaseDB 返回路由租约续期专用的连接池，与业务连接池相互隔离。
func (s *Store) LeaseDB() *bun.DB {
	return s.lease
}

// DB 返回服务端使用的 Bun 数据库连接。
func (s *Store) DB() *bun.DB {
	return s.db
}
