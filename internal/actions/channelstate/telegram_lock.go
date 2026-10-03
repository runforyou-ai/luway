//go:build server

// Package channelstate 串行化 Telegram 渠道配置操作。
package channelstate

import (
	"context"
	"database/sql/driver"
	"fmt"
	"github.com/uptrace/bun"
	"log/slog"
	"time"
)

// WithTelegramLock 在专用数据库连接上串行执行单个渠道的完整配置生命周期，并可靠释放会话锁。
func WithTelegramLock(ctx context.Context, db *bun.DB, channelID string, execute func(bun.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire Telegram channel connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended(?, 0))", channelID); err != nil {
		return fmt.Errorf("lock Telegram channel: %w", err)
	}
	defer func(conn bun.Conn, channelID string) {
		// 释放会话锁，释放失败时丢弃底层连接。
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := conn.ExecContext(releaseCtx, "SELECT pg_advisory_unlock(hashtextextended(?, 0))", channelID)
		if err == nil {
			return
		}
		slog.Warn("释放 Telegram 渠道锁失败，已丢弃数据库连接", "channel_id", channelID, "error", err)
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}(conn, channelID)
	return execute(conn)
}
