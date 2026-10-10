//go:build server

package agentrun

import (
	"context"
	"uuid"

	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
)

// runningAgentRun 是本实例正在执行的一次运行尝试：取消函数、尝试序号、准入序号与运行流。
type runningAgentRun struct {
	cancel    context.CancelFunc
	attempt   int
	admission uint64
	streamID  string
	stream    *realtime.RunStreamSource
}

// registerRunContext 注册一次可被运行结束通知中断的模型调用：订阅该运行的结束通知，收到后取消本次尝试的模型调用与进程内工具。
func (a *ExecuteAction) registerRunContext(ctx context.Context, workspaceID, runID string, cancel context.CancelFunc) (*runningAgentRun, func(), error) {
	execution, _ := servertask.CurrentExecution(ctx)
	running := &runningAgentRun{cancel: cancel, attempt: execution.Attempt, streamID: uuid.NewV7().String()}
	a.runningMu.Lock()
	if previous := a.runningRuns[runID]; previous != nil {
		if previous.attempt >= running.attempt {
			a.runningMu.Unlock()
			return nil, nil, servertask.ErrExecutionLost
		}
		previous.cancel()
	}
	a.admissions++
	running.admission = a.admissions
	a.runningRuns[runID] = running
	a.runningMu.Unlock()
	// 订阅运行结束通知需要等待总线确认生效，在锁外执行。
	unwatch := realtime.WatchAgentRunEnded(workspaceID, runID, cancel)
	running.stream = realtime.OpenRunStream(ctx, workspaceID, running.admission, realtime.RunSnapshot{RunID: runID, Attempt: running.attempt, Snapshot: stream.Snapshot{Stream: running.streamID}})
	return running, func() {
		unwatch()
		a.runningMu.Lock()
		if a.runningRuns[runID] == running {
			delete(a.runningRuns, runID)
		}
		a.runningMu.Unlock()
		running.stream.End()
	}, nil
}
