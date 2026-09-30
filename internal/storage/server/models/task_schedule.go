//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// TaskSchedule 表示服务端定时 Action 计划。
type TaskSchedule struct {
	bun.BaseModel `bun:"table:task_schedules,alias:ts"`

	ID             string          `bun:"id,pk"`
	ScheduleKey    string          `bun:"schedule_key"`
	ActionName     string          `bun:"action_name"`
	QueueName      string          `bun:"queue_name"`
	Payload        json.RawMessage `bun:"payload,type:jsonb"`
	CronExpression string          `bun:"cron_expression"`
	Timezone       string          `bun:"timezone"`
	Enabled        bool            `bun:"enabled"`
	MaxAttempts    int             `bun:"max_attempts"`
	NextRunAt      time.Time       `bun:"next_run_at"`
	LastEnqueuedAt *time.Time      `bun:"last_enqueued_at"`
	CreatedAt      time.Time       `bun:"created_at"`
	UpdatedAt      time.Time       `bun:"updated_at"`
}
