//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
)

// fakeMCPConnection 记录关闭次数的测试 MCP 连接。
type fakeMCPConnection struct{ closed *atomic.Int32 }

// Tools 返回空工具目录。
func (c *fakeMCPConnection) Tools(context.Context) ([]mcpintegration.Tool, error) { return nil, nil }

// Call 返回固定结果。
func (c *fakeMCPConnection) Call(context.Context, string, json.RawMessage) (string, error) {
	return "ok", nil
}

// Close 记录一次关闭。
func (c *fakeMCPConnection) Close() error {
	c.closed.Add(1)
	return nil
}

// TestDeviceMCPSessionsReuseDiscardAndRelease 验证同一运行复用连接，丢弃后重新建立，运行结束时关闭全部连接。
func TestDeviceMCPSessionsReuseDiscardAndRelease(t *testing.T) {
	var opened, closed atomic.Int32
	server := agentruntime.MCPServer{ID: "orders", Connect: func(context.Context) (agentruntime.MCPConnection, error) {
		opened.Add(1)
		return &fakeMCPConnection{closed: &closed}, nil
	}}
	sessions := newDeviceMCPSessions()
	ctx := context.Background()
	use := func(discard bool) {
		t.Helper()
		if _, done, err := sessions.use(ctx, "run", server); err != nil {
			t.Fatal(err)
		} else {
			done(discard)
		}
	}
	use(false)
	use(false)
	if opened.Load() != 1 || closed.Load() != 0 {
		t.Fatalf("reuse opened=%d closed=%d", opened.Load(), closed.Load())
	}
	use(true)
	if closed.Load() != 1 {
		t.Fatalf("discard closed=%d", closed.Load())
	}
	use(false)
	if opened.Load() != 2 {
		t.Fatalf("reconnect opened=%d", opened.Load())
	}
	sessions.release("run")
	if closed.Load() != 2 || len(sessions.runs) != 0 {
		t.Fatalf("release closed=%d runs=%d", closed.Load(), len(sessions.runs))
	}
}

// TestDeviceMCPSessionsOpenFailure 验证建立连接失败时不缓存连接，下次重新建立。
func TestDeviceMCPSessionsOpenFailure(t *testing.T) {
	var attempts atomic.Int32
	var closed atomic.Int32
	server := agentruntime.MCPServer{ID: "orders", Connect: func(context.Context) (agentruntime.MCPConnection, error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("unavailable")
		}
		return &fakeMCPConnection{closed: &closed}, nil
	}}
	sessions := newDeviceMCPSessions()
	if _, _, err := sessions.use(context.Background(), "run", server); err == nil {
		t.Fatal("open failure not reported")
	}
	if _, done, err := sessions.use(context.Background(), "run", server); err != nil {
		t.Fatal(err)
	} else {
		done(false)
	}
	sessions.release("run")
	if attempts.Load() != 2 || closed.Load() != 1 {
		t.Fatalf("attempts=%d closed=%d", attempts.Load(), closed.Load())
	}
}

// TestDeviceMCPSessionsStaleDiscard 验证并发调用中迟到的丢弃只关闭自己用过的连接，不影响随后建立的新连接。
func TestDeviceMCPSessionsStaleDiscard(t *testing.T) {
	var opened, closed atomic.Int32
	server := agentruntime.MCPServer{ID: "orders", Connect: func(context.Context) (agentruntime.MCPConnection, error) {
		opened.Add(1)
		return &fakeMCPConnection{closed: &closed}, nil
	}}
	sessions := newDeviceMCPSessions()
	ctx := context.Background()
	first, doneA, err := sessions.use(ctx, "run", server)
	if err != nil {
		t.Fatal(err)
	}
	_, doneB, err := sessions.use(ctx, "run", server)
	if err != nil {
		t.Fatal(err)
	}
	doneA(true)
	second, doneC, err := sessions.use(ctx, "run", server)
	if err != nil || second == first {
		t.Fatalf("reconnect=%v err=%v", second == first, err)
	}
	doneB(true)
	doneC(false)
	if closed.Load() != 1 {
		t.Fatalf("stale discard closed=%d", closed.Load())
	}
	third, doneD, err := sessions.use(ctx, "run", server)
	if err != nil || third != second || opened.Load() != 2 {
		t.Fatalf("reuse=%v opened=%d err=%v", third == second, opened.Load(), err)
	}
	doneD(false)
	sessions.release("run")
}

// TestDeviceMCPSessionsWaitRespectsCancel 验证等待其他调用建立连接时响应本次调用的取消。
func TestDeviceMCPSessionsWaitRespectsCancel(t *testing.T) {
	var closed atomic.Int32
	unblock := make(chan struct{})
	started := make(chan struct{})
	server := agentruntime.MCPServer{ID: "orders", Connect: func(context.Context) (agentruntime.MCPConnection, error) {
		close(started)
		<-unblock
		return &fakeMCPConnection{closed: &closed}, nil
	}}
	sessions := newDeviceMCPSessions()
	first := make(chan error, 1)
	go func() {
		_, done, err := sessions.use(context.Background(), "run", server)
		if err == nil {
			done(false)
		}
		first <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := sessions.use(ctx, "run", server); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting use err=%v", err)
	}
	close(unblock)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	sessions.release("run")
	if closed.Load() != 1 {
		t.Fatalf("closed=%d", closed.Load())
	}
}

// TestDeviceMCPSessionsReleaseDuringOpen 验证建立连接期间运行结束时，新连接被关闭而不缓存。
func TestDeviceMCPSessionsReleaseDuringOpen(t *testing.T) {
	var closed atomic.Int32
	unblock := make(chan struct{})
	started := make(chan struct{})
	server := agentruntime.MCPServer{ID: "orders", Connect: func(context.Context) (agentruntime.MCPConnection, error) {
		close(started)
		<-unblock
		return &fakeMCPConnection{closed: &closed}, nil
	}}
	sessions := newDeviceMCPSessions()
	opened := make(chan error, 1)
	go func() {
		_, _, err := sessions.use(context.Background(), "run", server)
		opened <- err
	}()
	<-started
	released := make(chan struct{})
	go func() {
		sessions.release("run")
		close(released)
	}()
	// 等待关闭流程标记运行已关闭后再完成握手。
	deadline := time.Now().Add(time.Second)
	for {
		sessions.mu.Lock()
		_, cached := sessions.runs["run"]
		sessions.mu.Unlock()
		if !cached || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(unblock)
	if err := <-opened; !errors.Is(err, errDeviceMCPReleased) {
		t.Fatalf("open during release err=%v", err)
	}
	<-released
	if closed.Load() != 1 {
		t.Fatalf("closed=%d", closed.Load())
	}
}
