//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// TaskOutbox 表示等待发布到 NATS 的任务消息。
type TaskOutbox struct {
	bun.BaseModel `bun:"table:task_outbox,alias:tob"`

	TaskRunID   string    `bun:"task_run_id,pk"`
	MessageID   string    `bun:"message_id"`
	QueueName   string    `bun:"queue_name"`
	Attempts    int       `bun:"attempts"`
	AvailableAt time.Time `bun:"available_at"`
	LastError   *string   `bun:"last_error"`
	CreatedAt   time.Time `bun:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at"`
}
