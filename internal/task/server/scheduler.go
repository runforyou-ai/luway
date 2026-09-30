//go:build server

package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"uuid"

	"github.com/robfig/cron/v3"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	schedulerPollInterval = time.Second
	// scheduleParseFailureBackoff 是存量计划解析失败后 next_run_at 的后推时长。
	scheduleParseFailureBackoff = 5 * time.Minute
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// syncSchedules 校验并同步由代码注册的服务端定时计划。
func (r *Runtime) syncSchedules(ctx context.Context) error {
	seen := make(map[string]struct{}, len(r.schedules))
	keys := make([]string, 0, len(r.schedules))
	for _, definition := range r.schedules {
		definition.Key = strings.TrimSpace(definition.Key)
		if _, exists := seen[definition.Key]; exists {
			return fmt.Errorf("task schedule %q registered more than once", definition.Key)
		}
		seen[definition.Key] = struct{}{}
		keys = append(keys, definition.Key)
		if err := r.syncSchedule(ctx, definition); err != nil {
			return err
		}
	}
	return r.disableMissingSchedules(ctx, keys)
}

// syncSchedule 同步一个代码定义的定时计划。
func (r *Runtime) syncSchedule(ctx context.Context, definition ScheduleDefinition) error {
	definition.Key = strings.TrimSpace(definition.Key)
	definition.ActionName = strings.TrimSpace(definition.ActionName)
	definition.Timezone = strings.TrimSpace(definition.Timezone)
	if !actionNamePattern.MatchString(definition.Key) {
		return fmt.Errorf("invalid task schedule key %q", definition.Key)
	}
	if _, exists := r.registry.lookup(definition.ActionName); !exists {
		return fmt.Errorf("task schedule %q references unregistered action %q", definition.Key, definition.ActionName)
	}
	options, err := normalizeEnqueueOptions(EnqueueOptions{
		Queue: definition.Queue, MaxAttempts: definition.MaxAttempts, TriggerType: TriggerSchedule,
	})
	if err != nil {
		return fmt.Errorf("invalid task schedule %q: %w", definition.Key, err)
	}
	definition.Queue = options.Queue
	definition.MaxAttempts = options.MaxAttempts
	if definition.Timezone == "" {
		definition.Timezone = "UTC"
	}
	schedule, err := parseSchedule(definition.CronExpression, definition.Timezone)
	if err != nil {
		return fmt.Errorf("parse task schedule %q: %w", definition.Key, err)
	}
	payload, err := encodePayload(definition.Payload)
	if err != nil {
		return fmt.Errorf("encode task schedule %q: %w", definition.Key, err)
	}

	now := time.Now().UTC()
	scheduledNextRunAt := schedule.Next(now)
	nextRunAt := scheduledNextRunAt
	if definition.StartImmediately && definition.Enabled {
		nextRunAt = now
	}
	record := &servermodels.TaskSchedule{
		ID: uuid.New().String(), ScheduleKey: definition.Key, ActionName: definition.ActionName,
		QueueName: definition.Queue, Payload: payload, CronExpression: definition.CronExpression,
		Timezone: definition.Timezone, Enabled: definition.Enabled, MaxAttempts: definition.MaxAttempts,
		NextRunAt: nextRunAt, CreatedAt: now, UpdatedAt: now,
	}
	_, err = r.repository.db.NewRaw(`
		INSERT INTO task_schedules (
			id, schedule_key, action_name, queue_name, payload, cron_expression,
			timezone, enabled, max_attempts, next_run_at, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (schedule_key) DO UPDATE SET
			action_name = EXCLUDED.action_name,
			queue_name = EXCLUDED.queue_name,
			payload = EXCLUDED.payload,
			cron_expression = EXCLUDED.cron_expression,
			timezone = EXCLUDED.timezone,
			enabled = EXCLUDED.enabled,
			max_attempts = EXCLUDED.max_attempts,
			next_run_at = CASE
				WHEN task_schedules.action_name = EXCLUDED.action_name
					AND task_schedules.queue_name = EXCLUDED.queue_name
					AND task_schedules.payload = EXCLUDED.payload
					AND task_schedules.cron_expression = EXCLUDED.cron_expression
					AND task_schedules.timezone = EXCLUDED.timezone
					AND task_schedules.enabled = EXCLUDED.enabled
					AND task_schedules.max_attempts = EXCLUDED.max_attempts
				THEN task_schedules.next_run_at
				WHEN task_schedules.enabled = FALSE
					AND EXCLUDED.enabled = TRUE
					AND ?
				THEN ?::timestamptz
				ELSE ?::timestamptz
			END,
			updated_at = EXCLUDED.updated_at
	`, record.ID, record.ScheduleKey, record.ActionName, record.QueueName, string(record.Payload),
		record.CronExpression, record.Timezone, record.Enabled, record.MaxAttempts,
		record.NextRunAt, record.CreatedAt, record.UpdatedAt,
		definition.StartImmediately, now, scheduledNextRunAt).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sync task schedule %q: %w", definition.Key, err)
	}
	return nil
}

// disableMissingSchedules 停用已经从代码注册表移除的计划。
func (r *Runtime) disableMissingSchedules(ctx context.Context, keys []string) error {
	now := time.Now().UTC()
	query := r.repository.db.NewUpdate().Table("task_schedules").
		Set("enabled = FALSE").
		Set("updated_at = ?", now).
		Where("enabled = TRUE")
	if len(keys) > 0 {
		query = query.Where("schedule_key NOT IN (?)", bun.In(keys))
	}
	result, err := query.Exec(ctx)
	if err != nil {
		return fmt.Errorf("disable unregistered task schedules: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read disabled task schedule count: %w", err)
	}
	if count > 0 {
		slog.Info("已停用未注册的定时任务", "count", count)
	}
	return nil
}

// parseSchedule 使用指定时区解析五段 Cron 或描述符表达式。
func parseSchedule(expression, timezone string) (cron.Schedule, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", timezone, err)
	}
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, errors.New("cron expression is required")
	}
	return cronParser.Parse("CRON_TZ=" + location.String() + " " + expression)
}

// runScheduler 持续认领到期计划并创建异步任务。
func (r *Runtime) runScheduler(ctx context.Context) {
	defer r.waitGroup.Done()
	ticker := time.NewTicker(schedulerPollInterval)
	defer ticker.Stop()
	for {
		triggered, err := r.triggerOneSchedule(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("触发定时任务失败", "namespace", r.config.Namespace, "error", err)
		}
		if triggered {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// triggerOneSchedule 在同一事务内推进计划并创建一次任务。
func (r *Runtime) triggerOneSchedule(ctx context.Context) (bool, error) {
	triggered := false
	err := r.repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		var record servermodels.TaskSchedule
		err := tx.NewSelect().Model(&record).
			Where("ts.enabled = TRUE").
			Where("ts.next_run_at <= ?", now).
			OrderExpr("ts.next_run_at ASC, ts.schedule_key ASC").
			For("UPDATE SKIP LOCKED").
			Limit(1).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim due task schedule: %w", err)
		}
		schedule, err := parseSchedule(record.CronExpression, record.Timezone)
		if err != nil {
			// 解析失败时后推 next_run_at 并提交事务；直接返回错误会导致回滚，
			// 这条坏计划每个轮询周期都会被重新认领，阻塞其后所有到期计划。
			slog.Error("解析存量定时计划失败，已后推下次运行时间",
				"schedule_key", record.ScheduleKey, "error", err)
			if _, deferErr := tx.NewRaw(`
				UPDATE task_schedules
				SET next_run_at = ?, updated_at = ?
				WHERE id = ?
			`, now.Add(scheduleParseFailureBackoff), now, record.ID).Exec(ctx); deferErr != nil {
				return fmt.Errorf("defer broken task schedule %q: %w", record.ScheduleKey, deferErr)
			}
			return nil
		}
		dueAt := record.NextRunAt.UTC()
		options := EnqueueOptions{
			Queue: record.QueueName, MaxAttempts: record.MaxAttempts,
			TriggerType:    TriggerSchedule,
			IdempotencyKey: record.ScheduleKey + ":" + dueAt.Format(time.RFC3339Nano),
		}
		if _, err := enqueueIn(ctx, tx, record.ActionName, record.Payload, options, record.ScheduleKey); err != nil {
			return err
		}
		nextRunAt := schedule.Next(now)
		if _, err := tx.NewRaw(`
			UPDATE task_schedules
			SET next_run_at = ?, last_enqueued_at = ?, updated_at = ?
			WHERE id = ?
		`, nextRunAt, dueAt, now, record.ID).Exec(ctx); err != nil {
			return fmt.Errorf("advance task schedule %q: %w", record.ScheduleKey, err)
		}
		triggered = true
		return nil
	})
	return triggered, err
}
