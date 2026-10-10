package server

import (
	"context"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// LockNamespace 是咨询锁的业务命名空间，锁键由命名空间与业务键拼接后经 hashtextextended 映射为 64 位编号；不同命名空间的锁键互不同名，哈希碰撞仍可能让两个锁键互相等待。
type LockNamespace string

const (
	// LockMessagePartitions 串行创建消息分区。
	LockMessagePartitions LockNamespace = "message-partitions"
	// LockServerLogPartitions 串行维护服务端日志分区。
	LockServerLogPartitions LockNamespace = "server-log-partitions"
	// LockPlatformStats 串行执行运营数据汇总与重建。
	LockPlatformStats LockNamespace = "platform-stats"
	// LockVectorIndexes 串行执行知识库专属向量索引对账。
	LockVectorIndexes LockNamespace = "vector-indexes"
	// LockDeploymentAdmission 串行执行服务端进程加入部署。
	LockDeploymentAdmission LockNamespace = "deployment-admission"
	// LockDeploymentCertificate 串行签发部署地址证书。
	LockDeploymentCertificate LockNamespace = "deployment-certificate"
	// LockServerInstance 是服务端进程运行期间持有的实例锁，键为实例编号。
	LockServerInstance LockNamespace = "server-instance"
	// LockPushDevice 串行登记同一推送设备，键为应用、平台与设备编号。
	LockPushDevice LockNamespace = "push-device"
	// LockPlatformContact 串行处理同一平台账号下同一外部编号的首次入站，键为工作区、平台账号与外部编号。
	LockPlatformContact LockNamespace = "platform-contact"
	// LockWeComBot 串行连接同一企业微信机器人，键为机器人编号。
	LockWeComBot LockNamespace = "wecom-bot"
	// LockTelegramChannel 串行执行单个 Telegram 渠道的配置生命周期，键为渠道编号。
	LockTelegramChannel LockNamespace = "telegram-channel"
	// LockTelegramBot 串行执行工作区内同一 Telegram Bot 的 Webhook 生命周期，键为工作区与 Bot 编号。
	LockTelegramBot LockNamespace = "telegram-bot"
)

// LockKey 用冒号连接多个部分，组成一个业务锁键。
func LockKey(parts ...string) string {
	return strings.Join(parts, ":")
}

// lockName 返回命名空间下键的完整锁名；空键的锁名是命名空间本身，这把锁与该命名空间下带键的锁互不排斥。
func lockName(namespace LockNamespace, key string) string {
	if key == "" {
		return string(namespace)
	}
	return string(namespace) + ":" + key
}

// lockNames 返回去重并升序排列的完整锁名，多个锁按同一顺序取得；未给出键时只返回以命名空间命名的一把锁，它与该命名空间下带键的锁互不排斥。
func lockNames(namespace LockNamespace, keys []string) []string {
	if len(keys) == 0 {
		return []string{string(namespace)}
	}
	names := arr.Map(keys, func(key string) string { return lockName(namespace, key) })
	slices.Sort(names)
	return slices.Compact(names)
}

// XactLock 在事务内按升序取得命名空间下各键的事务级咨询锁，未给出键时取得以命名空间命名的一把锁，锁随事务结束释放。
func XactLock(ctx context.Context, tx bun.IDB, namespace LockNamespace, keys ...string) error {
	for _, name := range lockNames(namespace, keys) {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", name); err != nil {
			return fmt.Errorf("lock %s: %w", namespace, err)
		}
	}
	return nil
}

// SessionLock 在连接上按升序取得命名空间下各键的会话级咨询锁；中途失败时释放已取得的锁后返回错误。
func SessionLock(ctx context.Context, conn bun.Conn, namespace LockNamespace, keys ...string) error {
	names := lockNames(namespace, keys)
	for index, name := range names {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended(?, 0))", name); err != nil {
			_ = releaseSessionLocks(ctx, conn, names[:index])
			return fmt.Errorf("lock %s: %w", namespace, err)
		}
	}
	return nil
}

// TrySessionLock 在连接上尝试取得命名空间下单个键的会话级咨询锁，空键取以命名空间命名的一把锁，锁被占用时立即返回 false。
func TrySessionLock(ctx context.Context, conn bun.Conn, namespace LockNamespace, key string) (bool, error) {
	var locked bool
	if err := conn.NewRaw("SELECT pg_try_advisory_lock(hashtextextended(?, 0))", lockName(namespace, key)).Scan(ctx, &locked); err != nil {
		return false, fmt.Errorf("try lock %s: %w", namespace, err)
	}
	return locked, nil
}

// SessionUnlock 按降序释放连接上命名空间下各键的会话级咨询锁，释放失败时丢弃底层连接并返回错误，PostgreSQL 随连接断开释放其余锁。
func SessionUnlock(ctx context.Context, conn bun.Conn, namespace LockNamespace, keys ...string) error {
	return releaseSessionLocks(ctx, conn, lockNames(namespace, keys))
}

// releaseSessionLocks 在独立的限时上下文中按降序释放会话级咨询锁，释放失败时记录警告、丢弃底层连接并返回错误。
func releaseSessionLocks(ctx context.Context, conn bun.Conn, names []string) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, name := range slices.Backward(names) {
		if _, err := conn.ExecContext(releaseCtx, "SELECT pg_advisory_unlock(hashtextextended(?, 0))", name); err != nil {
			slog.WarnContext(ctx, "释放咨询锁失败，已丢弃数据库连接", "lock", name, "error", err)
			DiscardConn(conn)
			return fmt.Errorf("unlock %s: %w", name, err)
		}
	}
	return nil
}

// DiscardConn 把连接标记为失效，连接关闭时从连接池丢弃，连接上的会话状态与会话级咨询锁随之释放。
func DiscardConn(conn bun.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

// HoldSessionLock 在已有连接上取得命名空间下各键的会话级咨询锁后执行 fn，结束后释放。
func HoldSessionLock(ctx context.Context, conn bun.Conn, namespace LockNamespace, keys []string, fn func() error) error {
	if err := SessionLock(ctx, conn, namespace, keys...); err != nil {
		return err
	}
	defer func() { _ = SessionUnlock(ctx, conn, namespace, keys...) }()
	return fn()
}

// WithSessionLock 在新的专用连接上取得命名空间下各键的会话级咨询锁后执行 fn，结束后释放锁并归还连接。
func WithSessionLock(ctx context.Context, db *bun.DB, namespace LockNamespace, keys []string, fn func(conn bun.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open %s lock connection: %w", namespace, err)
	}
	defer conn.Close()
	return HoldSessionLock(ctx, conn, namespace, keys, func() error { return fn(conn) })
}

// TryWithSessionLock 在新的专用连接上尝试取得单个会话级咨询锁，取得时执行 fn 后释放并返回 true，锁被占用时返回 false。
func TryWithSessionLock(ctx context.Context, db *bun.DB, namespace LockNamespace, key string, fn func(conn bun.Conn) error) (bool, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("open %s lock connection: %w", namespace, err)
	}
	defer conn.Close()
	locked, err := TrySessionLock(ctx, conn, namespace, key)
	if err != nil || !locked {
		return false, err
	}
	defer func() { _ = SessionUnlock(ctx, conn, namespace, key) }()
	return true, fn(conn)
}
