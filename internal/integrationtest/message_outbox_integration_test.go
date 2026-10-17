//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/messaging/outbox"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestMessageOutboxRecovery 验证回滚、保存点、重启投递和不确定发布保留相同登记。
func TestMessageOutboxRecovery(t *testing.T) {
	t.Parallel()
	db := openRealtimeDB(t)
	kind := "outbox-test-" + uuid.NewV7().String()
	aborted := errors.New("rollback")
	err := serverstorage.RunInTx(t.Context(), db, func(ctx context.Context, _ bun.Tx) error {
		_, err := serverstorage.PutOutbox(ctx, kind, map[string]string{"message": "rolled back"})
		require.NoError(t, err)
		return aborted
	})
	require.ErrorIs(t, err, aborted)
	var id string
	require.NoError(t, serverstorage.RunInTx(t.Context(), db, func(ctx context.Context, tx bun.Tx) error {
		require.ErrorIs(t, serverstorage.RunInTx(ctx, tx, func(ctx context.Context, _ bun.Tx) error {
			_, err := serverstorage.PutOutbox(ctx, kind, "savepoint")
			require.NoError(t, err)
			return aborted
		}), aborted)
		var err error
		id, err = serverstorage.PutOutbox(ctx, kind, map[string]string{"message": "committed"})
		return err
	}))
	var count int
	require.NoError(t, db.NewRaw("SELECT count(*) FROM message_outbox WHERE kind = ?", kind).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	attempts := 0
	uncertain := errors.New("publication acknowledgement lost")
	publish := func(_ context.Context, got string, raw json.RawMessage) error {
		require.Equal(t, id, got)
		require.JSONEq(t, `{"message":"committed"}`, string(raw))
		attempts++
		if attempts == 1 {
			return uncertain
		}
		return nil
	}
	first := outbox.New(db, kind, publish)
	found, err := first.Step(t.Context())
	require.True(t, found)
	require.ErrorIs(t, err, uncertain)
	require.NoError(t, db.NewRaw("SELECT count(*) FROM message_outbox WHERE id = ? AND attempts = 1", id).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	_, err = db.NewRaw("UPDATE message_outbox SET available_at = now() WHERE id = ?", id).Exec(t.Context())
	require.NoError(t, err)
	restarted := outbox.New(db, kind, publish)
	found, err = restarted.Step(t.Context())
	require.True(t, found)
	require.NoError(t, err)
	found, err = restarted.Step(t.Context())
	require.False(t, found)
	require.NoError(t, err)
	require.Equal(t, 2, attempts)
}

// TestMessageOutboxConcurrentClaim 验证多个投递器不会同时发布同一条登记。
func TestMessageOutboxConcurrentClaim(t *testing.T) {
	t.Parallel()
	db := openRealtimeDB(t)
	kind := "outbox-concurrent-" + uuid.NewV7().String()
	require.NoError(t, serverstorage.RunInTx(t.Context(), db, func(ctx context.Context, _ bun.Tx) error {
		_, err := serverstorage.PutOutbox(ctx, kind, "payload")
		return err
	}))
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	first := outbox.New(db, kind, func(context.Context, string, json.RawMessage) error { close(entered); <-release; return nil })
	go func() { _, err := first.Step(t.Context()); done <- err }()
	<-entered
	second := outbox.New(db, kind, func(context.Context, string, json.RawMessage) error {
		t.Error("duplicate concurrent publication")
		return nil
	})
	found, err := second.Step(t.Context())
	close(release)
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, <-done)
}

// waitRealtimeOutbox 等待测试身份建立时的历史通知完成发布后再建立观察点。
// 实时 outbox 按 250ms 轮询且全库共用 kind=realtime 队列，CI 并行与库争用下清空可超过 10s。
func waitRealtimeOutbox(t *testing.T, db *bun.DB, workspaceID string) {
	t.Helper()
	pattern, err := json.Marshal([]map[string]string{{"WorkspaceID": workspaceID}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var count int
		err := db.NewRaw("SELECT count(*) FROM message_outbox WHERE kind = 'realtime' AND payload @> ?::jsonb", string(pattern)).Scan(t.Context(), &count)
		return err == nil && count == 0
	}, 30*time.Second, 20*time.Millisecond)
}
