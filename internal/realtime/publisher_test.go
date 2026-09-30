//go:build server

package realtime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

// lockedBuffer 串行写入和读取测试日志。
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

// Write 追加一段日志输出。
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

// String 返回当前全部日志输出。
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// TestPublisherKeepsCommitsNonBlocking 验证发布阻塞时事务提交照常返回，队列已满与发布失败记录 WARN。
func TestPublisherKeepsCommitsNonBlocking(t *testing.T) {
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	logs := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	publisher := NewPublisher(serverconfig.NATSConfig{Namespace: "test_realtime_blocked"})
	// 发送函数在测试放行前阻塞，放行后返回发布失败。
	publisher.begin(func(string, []byte) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return errors.New("publish rejected")
	})
	t.Cleanup(func() { _ = publisher.Stop() })
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	// commit 提交只登记一条通知的事务，5 秒内未返回即判定提交等待了发布。
	commit := func(audienceID string) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			done <- RunInTx(ctx, store.DB(), func(ctx context.Context, _ bun.Tx) error {
				Notify(ctx, UserConversationChanged("organization", audienceID, "conversation", domain.ConversationTypeDirect, 1, domain.ConversationChangeTimeline))
				return nil
			})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("提交 %s 等待发布超过 5 秒", audienceID)
		}
	}

	commit("first")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("发布协程未开始发送")
	}
	// 发送阻塞期间提交照常返回。
	commit("while-blocked")
	// 填满剩余队列后提交仍返回，通知被丢弃。
	for range publishQueueSize - 1 {
		publisher.enqueue([]Notification{UserConversationChanged("organization", "filler", "conversation", domain.ConversationTypeDirect, 1, domain.ConversationChangeTimeline)})
	}
	commit("overflow")
	if output := logs.String(); !strings.Contains(output, "实时通知发布队列已满") {
		t.Fatalf("缺少队列已满日志: %s", output)
	}
	// 放行并停止发布器后，已开始发送的通知记录发布失败。
	releaseOnce.Do(func() { close(release) })
	if err := publisher.Stop(); err != nil {
		t.Fatal(err)
	}
	if output := logs.String(); !strings.Contains(output, "实时通知发布失败") || !strings.Contains(output, "audience_id=first") {
		t.Fatalf("缺少发布失败日志: %s", output)
	}
}
