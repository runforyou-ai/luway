//go:build server

package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"uuid"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
	"github.com/runforyou-ai/cervi/internal/servertest"
	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestSyncScheduleWithPostgreSQL 验证计划同步的时间类型和下一执行时间保留规则。
func TestSyncScheduleWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	databaseConfig := servertest.DatabaseConfig(t)
	store, err := serverstorage.Open(ctx, databaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	scheduleKey := "test.schedule." + uuid.New().String()
	db := store.DB()
	t.Cleanup(func() {
		if _, cleanupErr := db.NewDelete().Table("task_schedules").
			Where("schedule_key = ?", scheduleKey).
			Exec(context.Background()); cleanupErr != nil {
			t.Errorf("清理测试计划失败: %v", cleanupErr)
		}
	})

	runtime := New(db, serverconfig.NATSConfig{})
	const actionName = "test.schedule.action"
	if err := runtime.Registry().Register(actionName, func(context.Context, json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	definition := ScheduleDefinition{
		Key: scheduleKey, ActionName: actionName, Queue: "maintenance", Payload: struct{}{},
		CronExpression: "@hourly", Timezone: "UTC", Enabled: true, MaxAttempts: 3, StartImmediately: true,
	}
	readSchedule := func() servermodels.TaskSchedule {
		var record servermodels.TaskSchedule
		if err := db.NewSelect().Model(&record).Where("ts.schedule_key = ?", scheduleKey).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return record
	}

	firstSyncStartedAt := time.Now().UTC()
	if err := runtime.syncSchedule(ctx, definition); err != nil {
		t.Fatal(err)
	}
	first := readSchedule()
	if first.NextRunAt.Before(firstSyncStartedAt.Add(-time.Second)) || first.NextRunAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("首次立即执行时间 = %s", first.NextRunAt)
	}

	if err := runtime.syncSchedule(ctx, definition); err != nil {
		t.Fatal(err)
	}
	unchanged := readSchedule()
	if !unchanged.NextRunAt.Equal(first.NextRunAt) {
		t.Fatalf("未变化计划的下次执行时间 = %s，期望保留 %s", unchanged.NextRunAt, first.NextRunAt)
	}

	disabled := definition
	disabled.Enabled = false
	if err := runtime.syncSchedule(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	disabledRecord := readSchedule()
	if !disabledRecord.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("停用计划的下次执行时间 = %s，期望未来时间", disabledRecord.NextRunAt)
	}

	reenabledAt := time.Now().UTC()
	if err := runtime.syncSchedule(ctx, definition); err != nil {
		t.Fatal(err)
	}
	reenabled := readSchedule()
	if reenabled.NextRunAt.Before(reenabledAt.Add(-time.Second)) || reenabled.NextRunAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("重新启用计划的立即执行时间 = %s", reenabled.NextRunAt)
	}
}
