//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ServerLog 表示一条服务端日志。
type ServerLog struct {
	bun.BaseModel `bun:"table:server_logs,alias:sl"`

	ID          string            `bun:"id,pk"`
	OccurredAt  time.Time         `bun:"occurred_at,pk"`
	CreatedAt   time.Time         `bun:"created_at,nullzero,default:now()"`
	UpdatedAt   time.Time         `bun:"updated_at,nullzero,default:now()"`
	Level       int               `bun:"level"`
	InstanceID  string            `bun:"instance_id"`
	Hostname    string            `bun:"hostname"`
	Version     string            `bun:"version"`
	Message     string            `bun:"message"`
	TraceID     *string           `bun:"trace_id"`
	Operation   *string           `bun:"operation"`
	TaskRunID   *string           `bun:"task_run_id"`
	Action      *string           `bun:"action"`
	Queue       *string           `bun:"queue"`
	WorkspaceID *string           `bun:"workspace_id"`
	AccountID   *string           `bun:"account_id"`
	Error       *string           `bun:"error"`
	EventID     *string           `bun:"event_id"`
	Attributes  map[string]string `bun:"attributes,type:jsonb"`
}
