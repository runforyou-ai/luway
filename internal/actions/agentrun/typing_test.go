//go:build server

package agentrun

import (
	"context"
	"sync"
	"testing"
	"time"
)

// typingRecorder 记录输入状态发布序列。
type typingRecorder struct {
	mu     sync.Mutex
	events []bool
}

// publish 追加一次发布。
func (r *typingRecorder) publish(active bool) {
	r.mu.Lock()
	r.events = append(r.events, active)
	r.mu.Unlock()
}

// last 返回最后一次发布与发布次数。
func (r *typingRecorder) last() (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return false, 0
	}
	return r.events[len(r.events)-1], len(r.events)
}

// TestRunTypingConcurrentClose 验证并发重复停止只发布一次停止输入。
func TestRunTypingConcurrentClose(t *testing.T) {
	recorder := &typingRecorder{}
	typing := startRunTyping(context.Background(), recorder.publish, time.Hour)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			typing.close()
		}()
	}
	wg.Wait()
	if active, count := recorder.last(); active || count != 2 {
		t.Fatalf("events=%v", recorder.events)
	}
}

// TestRunTypingContextDone 验证运行 context 结束后发布停止输入。
func TestRunTypingContextDone(t *testing.T) {
	recorder := &typingRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	typing := startRunTyping(ctx, recorder.publish, 10*time.Millisecond)
	cancel()
	select {
	case <-typing.done:
	case <-time.After(2 * time.Second):
		t.Fatal("typing did not stop after context done")
	}
	if active, _ := recorder.last(); active {
		t.Fatalf("events=%v", recorder.events)
	}
	typing.close()
}
