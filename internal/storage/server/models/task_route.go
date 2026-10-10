//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// TaskRoute 表示一个任务路由的租约，带该路由键的任务只由持有有效租约的服务端实例认领。
type TaskRoute struct {
	bun.BaseModel `bun:"table:task_routes,alias:trt"`

	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
	RouteKey       string    `bun:"route_key,pk"`
	InstanceID     string    `bun:"instance_id"`
	LeaseExpiresAt time.Time `bun:"lease_expires_at"`
}
