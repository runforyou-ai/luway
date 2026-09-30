//go:build server

package server

import (
	"context"
	"errors"
	"testing"

	"github.com/uptrace/bun"
)

// TestExecutionLeaseFencing 验证租约接管和最终失败收尾均拒绝过期尝试。
func TestExecutionLeaseFencing(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	runID, err := runtime.Enqueue(ctx, registerEnqueueTestAction(t, runtime), struct{}{}, EnqueueOptions{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	cleanupEnqueuedTask(t, db, runID)
	first, err := runtime.repository.claimRun(ctx, runID, "worker-before-restart")
	if err != nil || first == nil {
		t.Fatalf("first claim = %#v, error = %v", first, err)
	}
	check := func(executionCtx context.Context, wantLost bool) {
		t.Helper()
		err := db.RunInTx(executionCtx, nil, func(ctx context.Context, tx bun.Tx) error { return LockExecution(ctx, tx) })
		if wantLost && !errors.Is(err, ErrExecutionLost) || !wantLost && err != nil {
			t.Fatalf("execution lock error = %v, want lost = %v", err, wantLost)
		}
	}
	check(withExecution(ctx, first, false, false), false)
	if _, err := db.ExecContext(ctx, "UPDATE task_runs SET lease_expires_at = now() - interval '1 second' WHERE id = ?", runID); err != nil {
		t.Fatal(err)
	}
	check(withExecution(ctx, first, false, false), true)
	second, err := runtime.repository.claimRun(ctx, runID, "worker-after-restart")
	if err != nil || second == nil || second.Attempt != 2 {
		t.Fatalf("second claim = %#v, error = %v", second, err)
	}
	check(withExecution(ctx, first, false, false), true)
	check(withExecution(ctx, first, true, false), true)
	check(withExecution(ctx, second, false, false), false)
	check(withExecution(ctx, second, true, true), true)
	if _, err := db.ExecContext(ctx, "UPDATE task_runs SET lease_expires_at = now() - interval '1 second' WHERE id = ?", runID); err != nil {
		t.Fatal(err)
	}
	check(withExecution(ctx, second, true, true), false)
}
