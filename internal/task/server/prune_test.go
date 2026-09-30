//go:build server

package server

import (
	"testing"
	"time"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestPruneTerminalRuns 验证只删除完成时间早于保留期的成功和失败运行。
func TestPruneTerminalRuns(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	now := time.Now().UTC()
	cutoff := now.Add(-30 * 24 * time.Hour)
	cases := []struct {
		status    string
		completed *time.Time
		kept      bool
	}{
		{status: statusSucceeded, completed: new(now.Add(-60 * 24 * time.Hour)), kept: false},
		{status: statusFailed, completed: new(now.Add(-60 * 24 * time.Hour)), kept: false},
		{status: statusSucceeded, completed: new(now), kept: true},
		{status: statusRetrying, completed: nil, kept: true},
	}
	runIDs := make([]string, len(cases))
	for index, item := range cases {
		runID, err := runtime.Enqueue(ctx, actionName, struct{}{}, EnqueueOptions{})
		if err != nil {
			t.Fatal(err)
		}
		cleanupEnqueuedTask(t, db, runID)
		if _, err := db.NewUpdate().Model((*servermodels.TaskRun)(nil)).
			Set("status = ?", item.status).Set("completed_at = ?", item.completed).Set("created_at = ?", now.Add(-90*24*time.Hour)).
			Where("id = ?", runID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		runIDs[index] = runID
	}

	if err := runtime.repository.pruneTerminalRuns(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	for index, item := range cases {
		exists, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("id = ?", runIDs[index]).Exists(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if exists != item.kept {
			t.Fatalf("status=%s kept=%v exists=%v", item.status, item.kept, exists)
		}
	}
}
