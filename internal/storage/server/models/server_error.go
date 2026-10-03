//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ServerError 表示一条服务端 Error 级别日志。
type ServerError struct {
	bun.BaseModel `bun:"table:server_errors,alias:se"`

	ID         string            `bun:"id,pk"`
	OccurredAt time.Time         `bun:"occurred_at"`
	InstanceID string            `bun:"instance_id"`
	Hostname   string            `bun:"hostname"`
	Version    string            `bun:"version"`
	Message    string            `bun:"message"`
	Operation  *string           `bun:"operation"`
	Action     *string           `bun:"action"`
	Queue      *string           `bun:"queue"`
	Error      *string           `bun:"error"`
	EventID    *string           `bun:"event_id"`
	Attributes map[string]string `bun:"attributes,type:jsonb"`
}
