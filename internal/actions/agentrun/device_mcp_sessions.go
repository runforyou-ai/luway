//go:build server

package agentrun

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
)

// deviceMCPIdleTimeout 是设备运行的企业 MCP 连接无调用后保留的时长，覆盖模型多轮生成之间的间隔；运行在其他实例结束时由它兜底关闭。
const deviceMCPIdleTimeout = 5 * time.Minute

// errDeviceMCPReleased 表示运行的企业 MCP 连接已随运行结束释放。
var errDeviceMCPReleased = errors.New("device agent run MCP connections released")

// deviceMCPSessions 按运行缓存代理设备调用的企业 MCP 连接，运行结束或空闲超时时关闭。
type deviceMCPSessions struct {
	mu   sync.Mutex
	runs map[string]*deviceRunMCP
}

// deviceRunMCP 保存一次设备运行已建立的企业 MCP 连接；服务槽位建立后保留到运行关闭。
type deviceRunMCP struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	servers  map[string]*deviceMCPServerSession
	inflight int
	idle     *time.Timer
	closed   bool
}

// deviceMCPServerSession 保存单个企业 MCP 服务的连接，建立、丢弃与关闭连接时持有 lock。
type deviceMCPServerSession struct {
	// lock 容量为 1，等待时可响应调用方取消。
	lock       chan struct{}
	connection agentruntime.MCPConnection
	cancel     context.CancelFunc
}

// deviceMCPOpenResult 是一次建立企业 MCP 连接的结果。
type deviceMCPOpenResult struct {
	connection agentruntime.MCPConnection
	err        error
}

// closeConnection 在持有 lock 时关闭连接后结束连接 context。
func (s *deviceMCPServerSession) closeConnection() error {
	if s.connection == nil {
		return nil
	}
	err := s.connection.Close()
	s.cancel()
	s.connection, s.cancel = nil, nil
	return err
}

// newDeviceMCPSessions 创建设备运行企业 MCP 连接缓存。
func newDeviceMCPSessions() *deviceMCPSessions {
	return &deviceMCPSessions{runs: make(map[string]*deviceRunMCP)}
}

// use 返回运行的企业 MCP 服务连接并登记一次进行中的使用，调用方以 done 结束使用；done 的 discard 为 true 时关闭本次使用的连接。
func (s *deviceMCPSessions) use(ctx context.Context, runID string, server agentruntime.MCPServer) (agentruntime.MCPConnection, func(discard bool), error) {
	run := s.acquire(runID)
	run.mu.Lock()
	slot := run.servers[server.ID]
	if slot == nil {
		slot = &deviceMCPServerSession{lock: make(chan struct{}, 1)}
		run.servers[server.ID] = slot
	}
	runCtx := run.ctx
	run.mu.Unlock()
	select {
	case slot.lock <- struct{}{}:
	case <-ctx.Done():
		s.finish(runID, run, nil, nil, false)
		return nil, nil, ctx.Err()
	}
	defer func() { <-slot.lock }()
	if err := ctx.Err(); err != nil {
		s.finish(runID, run, nil, nil, false)
		return nil, nil, err
	}
	if connection := slot.connection; connection != nil {
		return connection, func(discard bool) { s.finish(runID, run, slot, connection, discard) }, nil
	}
	// 连接 context 跟随运行缓存，建立连接的等待期限由调用方 context 控制，超时后关闭迟到返回的连接。
	sessionCtx, cancelSession := context.WithCancel(runCtx)
	opened := make(chan deviceMCPOpenResult, 1)
	go func() {
		connection, err := server.Open(sessionCtx)
		opened <- deviceMCPOpenResult{connection: connection, err: err}
	}()
	var result deviceMCPOpenResult
	select {
	case result = <-opened:
	case <-ctx.Done():
		cancelSession()
		go func() {
			if late := <-opened; late.connection != nil {
				_ = late.connection.Close()
			}
		}()
		s.finish(runID, run, nil, nil, false)
		return nil, nil, ctx.Err()
	}
	if result.err != nil {
		cancelSession()
		s.finish(runID, run, nil, nil, false)
		return nil, nil, result.err
	}
	// 运行已关闭时不缓存新连接；未关闭时关闭流程会等待 lock 后关闭这条连接。
	run.mu.Lock()
	closed := run.closed
	run.mu.Unlock()
	if closed {
		_ = result.connection.Close()
		cancelSession()
		s.finish(runID, run, nil, nil, false)
		return nil, nil, errDeviceMCPReleased
	}
	slot.connection, slot.cancel = result.connection, cancelSession
	connection := result.connection
	return connection, func(discard bool) { s.finish(runID, run, slot, connection, discard) }, nil
}

// acquire 取得运行的连接缓存并登记一次进行中的使用。
func (s *deviceMCPSessions) acquire(runID string) *deviceRunMCP {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run == nil {
		ctx, cancel := context.WithCancel(context.Background())
		run = &deviceRunMCP{ctx: ctx, cancel: cancel, servers: make(map[string]*deviceMCPServerSession)}
		s.runs[runID] = run
	}
	run.mu.Lock()
	run.inflight++
	if run.idle != nil {
		run.idle.Stop()
	}
	run.mu.Unlock()
	return run
}

// finish 结束一次使用：discard 为 true 且槽位仍是本次使用的连接时关闭该连接；运行没有进行中的使用时开始空闲计时。
func (s *deviceMCPSessions) finish(runID string, run *deviceRunMCP, slot *deviceMCPServerSession, connection agentruntime.MCPConnection, discard bool) {
	if discard && slot != nil {
		slot.lock <- struct{}{}
		if slot.connection == connection {
			_ = slot.closeConnection()
		}
		<-slot.lock
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	run.inflight--
	if run.inflight == 0 && !run.closed {
		run.idle = time.AfterFunc(deviceMCPIdleTimeout, func() { s.closeRun(runID, run, true) })
	}
}

// release 关闭运行的全部企业 MCP 连接。
func (s *deviceMCPSessions) release(runID string) {
	s.mu.Lock()
	run := s.runs[runID]
	s.mu.Unlock()
	if run != nil {
		s.closeRun(runID, run, false)
	}
}

// closeRun 从缓存移除运行并关闭其连接，onlyIdle 为 true 时运行仍有进行中的使用则保留。
func (s *deviceMCPSessions) closeRun(runID string, run *deviceRunMCP, onlyIdle bool) {
	// 与 acquire 相同按缓存、运行的顺序加锁，确认空闲与标记关闭在同一临界区内完成。
	s.mu.Lock()
	run.mu.Lock()
	if run.closed || (onlyIdle && run.inflight > 0) {
		run.mu.Unlock()
		s.mu.Unlock()
		return
	}
	if s.runs[runID] == run {
		delete(s.runs, runID)
	}
	run.closed = true
	if run.idle != nil {
		run.idle.Stop()
	}
	servers := make(map[string]*deviceMCPServerSession, len(run.servers))
	for serverID, slot := range run.servers {
		servers[serverID] = slot
	}
	run.mu.Unlock()
	s.mu.Unlock()
	for serverID, slot := range servers {
		slot.lock <- struct{}{}
		if err := slot.closeConnection(); err != nil {
			slog.Warn("关闭设备运行的企业 MCP 连接失败", "agent_run_id", runID, "mcp_server_id", serverID, "error", err)
		}
		<-slot.lock
	}
	run.cancel()
}
