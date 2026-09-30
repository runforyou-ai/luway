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

// TestRunTypingConcurrentClose 验证并发重复停止只发布一次停止输入，停止后不可再续期。
func TestRunTypingConcurrentClose(t *testing.T) {
	recorder := &typingRecorder{}
	typing := startRunTyping(context.Background(), recorder.publish, time.Time{}, time.Hour)
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
	if typing.extend(time.Now().Add(time.Hour)) {
		t.Fatal("extended closed typing")
	}
}

// TestRunTypingDeadline 验证截止时间到达后发布停止输入并拒绝续期，续期可推迟截止时间。
func TestRunTypingDeadline(t *testing.T) {
	recorder := &typingRecorder{}
	typing := startRunTyping(context.Background(), recorder.publish, time.Now().Add(30*time.Millisecond), 10*time.Millisecond)
	if !typing.extend(time.Now().Add(80 * time.Millisecond)) {
		t.Fatal("extend before deadline failed")
	}
	select {
	case <-typing.done:
	case <-time.After(2 * time.Second):
		t.Fatal("typing did not stop at deadline")
	}
	active, count := recorder.last()
	if active || count < 3 {
		t.Fatalf("events=%v", recorder.events)
	}
	if typing.extend(time.Now().Add(time.Hour)) {
		t.Fatal("extended expired typing")
	}
	typing.close()
}
