//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// TaskRun 表示服务端 Action 的一次异步运行。
type TaskRun struct {
	bun.BaseModel `bun:"table:task_runs,alias:tr"`

	ID             string          `bun:"id,pk"`
	ActionName     string          `bun:"action_name"`
	QueueName      string          `bun:"queue_name"`
	Payload        json.RawMessage `bun:"payload,type:jsonb"`
	TriggerType    string          `bun:"trigger_type"`
	ScheduleKey    *string         `bun:"schedule_key"`
	Status         string          `bun:"status"`
	Attempt        int             `bun:"attempt"`
	MaxAttempts    int             `bun:"max_attempts"`
	AvailableAt    time.Time       `bun:"available_at"`
	LeaseExpiresAt *time.Time      `bun:"lease_expires_at"`
	WorkerID       *string         `bun:"worker_id"`
	IdempotencyKey *string         `bun:"idempotency_key"`
	PublishedAt    *time.Time      `bun:"published_at"`
	StartedAt      *time.Time      `bun:"started_at"`
	CompletedAt    *time.Time      `bun:"completed_at"`
	LastError      *string         `bun:"last_error"`
	CreatedAt      time.Time       `bun:"created_at"`
	UpdatedAt      time.Time       `bun:"updated_at"`
}
