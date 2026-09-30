//go:build server

package agentrun

import (
	"context"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

type runningAgentRun struct {
	cancel   context.CancelFunc
	attempt  int
	streamID string
	stream   *runstream.Hub
}

// CancelForServiceSession 在客服事务内取消原负责人尚未结束的运行。
func (a *ExecuteAction) CancelForServiceSession(ctx context.Context, db bun.IDB, organizationID, serviceSessionID, agentIdentityID string, reason domain.AgentRunErrorCode) ([]string, error) {
	return chatstate.CancelServiceSessionRuns(ctx, db, organizationID, serviceSessionID, agentIdentityID, reason)
}

// CancelRunContexts 尽力取消本进程中正在执行的模型调用。
func (a *ExecuteAction) CancelRunContexts(runIDs []string) {
	a.runningMu.Lock()
	defer a.runningMu.Unlock()
	for _, runID := range runIDs {
		if running := a.runningRuns[runID]; running != nil {
			running.cancel()
		}
	}
}

// registerRunContext 注册一次可被客服负责人变化中断的模型调用。
func (a *ExecuteAction) registerRunContext(ctx context.Context, runID string, cancel context.CancelFunc) (*runningAgentRun, func(), error) {
	execution, _ := servertask.CurrentExecution(ctx)
	streamID := uuid.NewV7().String()
	running := &runningAgentRun{cancel: cancel, attempt: execution.Attempt, streamID: streamID,
		stream: runstream.NewHub(runstream.Snapshot{RunID: runID, StreamID: streamID, Attempt: execution.Attempt})}
	a.runningMu.Lock()
	if previous := a.runningRuns[runID]; previous != nil {
		if previous.attempt >= running.attempt {
			a.runningMu.Unlock()
			return nil, nil, servertask.ErrExecutionLost
		}
		previous.cancel()
	}
	a.runningRuns[runID] = running
	a.runningMu.Unlock()
	return running, func() {
		a.runningMu.Lock()
		if a.runningRuns[runID] == running {
			delete(a.runningRuns, runID)
		}
		a.runningMu.Unlock()
		running.stream.End()
	}, nil
}
