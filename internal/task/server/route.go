//go:build server

package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// Routes 管理服务端实例对任务路由的租约：持有路由租约的实例执行带该路由的任务。
type Routes struct {
	db         *bun.DB
	instanceID string
}

// NewRoutes 创建路由租约管理，db 是续期路由租约专用的连接池，instanceID 是本进程的实例编号。
func NewRoutes(db *bun.DB, instanceID string) *Routes {
	return &Routes{db: db, instanceID: instanceID}
}

// InstanceID 返回本进程的实例编号。
func (r *Routes) InstanceID() string {
	return r.instanceID
}

// HoldRoute 经续租连接池在路由租约空缺、已过期或已由本实例持有时为本实例取得或续约租约，有效期为 lease，返回本实例是否持有租约。
func (r *Routes) HoldRoute(ctx context.Context, key string, lease time.Duration) (bool, error) {
	var holder string
	err := r.db.NewRaw(`
		INSERT INTO task_routes (route_key, instance_id, lease_expires_at)
		VALUES (?, ?, now() + make_interval(secs => ?))
		ON CONFLICT (route_key) DO UPDATE SET
			instance_id = EXCLUDED.instance_id, lease_expires_at = EXCLUDED.lease_expires_at
		WHERE task_routes.instance_id = EXCLUDED.instance_id OR task_routes.lease_expires_at <= now()
		RETURNING instance_id::text
	`, key, r.instanceID, lease.Seconds()).Scan(ctx, &holder)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("hold task route: %w", err)
	}
	return holder == r.instanceID, nil
}

// ReleaseRoute 删除本实例持有的路由租约，其他实例随后可取得。
func (r *Routes) ReleaseRoute(ctx context.Context, key string) error {
	if _, err := r.db.NewDelete().TableExpr("task_routes").Where("route_key = ? AND instance_id = ?", key, r.instanceID).Exec(ctx); err != nil {
		return fmt.Errorf("release task route: %w", err)
	}
	return nil
}

// RouteHeldBy 返回路由键为 key 的有效租约由 instanceID 持有的查询条件。
func RouteHeldBy(key, instanceID string) schema.QueryWithArgs {
	return schema.SafeQuery("EXISTS (SELECT 1 FROM task_routes AS route WHERE route.route_key = ? AND route.instance_id = ? AND route.lease_expires_at > now())", []any{key, instanceID})
}

// RouteHeld 返回路由键为 key 的有效租约由任一实例持有的查询条件。
func RouteHeld(key string) schema.QueryWithArgs {
	return schema.SafeQuery("EXISTS (SELECT 1 FROM task_routes AS route WHERE route.route_key = ? AND route.lease_expires_at > now())", []any{key})
}

// pruneExpired 按间隔删除过期的路由租约，直到 ctx 结束。
func (r *Routes) pruneExpired(ctx context.Context) {
	ticker := time.NewTicker(routePruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if _, err := r.db.NewDelete().TableExpr("task_routes").Where("lease_expires_at <= now()").Exec(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "删除过期任务路由租约失败", "error", err)
		}
	}
}
