//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
)

// TestAgentRunShutdownResume 验证最后一次执行尝试在停机时中断后，按原尝试序号重新投递的任务由新实例接手并完成运行。
func TestAgentRunShutdownResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, _, modelID := newAIWorkspace(t)
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{DisplayName: "停机恢复", Execution: agentaction.ExecutionInput{
		Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "回复"},
	}})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	_, run := createAgentLockChat(t, ctx, db, identity, agent.IdentityID, tasks)
	_, err = db.NewUpdate().Model(&run).Set("task_run_id = NULL").WherePK().Exec(ctx)
	require.NoError(t, err)

	broker := servertest.StartRealtimeBroker(t)
	options := servertask.Options{Namespace: "resume-" + uuid.NewV7().String(), Replicas: 1}
	first, err := servertask.New(ctx, broker.JS, options, db, db, uuid.NewV7().String())
	require.NoError(t, err)
	entered := make(chan struct{})
	blocking := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, _ einorun.Feed) (agentruntime.RunResult, error) {
		close(entered)
		<-ctx.Done()
		return agentruntime.RunResult{}, ctx.Err()
	}}
	interrupted := newTestAgentRun(db, tasks, blocking, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	// 前两次尝试在执行前失败，第三次尝试进入执行后停机。
	starts := 0
	require.NoError(t, first.Registry().RegisterJSONWithTerminalFailure(agentrunaction.RunActionName, func(ctx context.Context, input agentrunaction.RunInput) error {
		starts++
		if starts < 3 {
			return servertask.RetryAfter(10*time.Millisecond, errors.New("temporary preflight failure"))
		}
		return interrupted.Execute(ctx, input)
	}, interrupted.FinalizeFailure))
	require.NoError(t, first.Start(ctx))
	t.Cleanup(first.Stop)
	require.NoError(t, first.Enqueue(ctx, agentrunaction.RunActionName, agentrunaction.RunInput{RunID: run.ID}, servertask.EnqueueOptions{
		WorkspaceID: identity.Workspace.ID, Queue: servertask.QueueAgent, MaxAttempts: 3,
	}))
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("last attempt did not start")
	}
	first.Stop()

	second, err := servertask.New(ctx, broker.JS, options, db, db, uuid.NewV7().String())
	require.NoError(t, err)
	success := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 1)
		return agentruntime.RunResult{Content: "恢复完成", EndSeq: claimed.EndSeq}, err
	}}
	resumed := newTestAgentRun(db, tasks, success, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	require.NoError(t, second.Registry().RegisterJSONWithTerminalFailure(agentrunaction.RunActionName, resumed.Execute, resumed.FinalizeFailure))
	require.NoError(t, second.Start(ctx))
	t.Cleanup(second.Stop)
	require.Eventually(t, func() bool {
		require.NoError(t, db.NewSelect().Model(&run).WherePK().Scan(ctx))
		return run.Status == string(domain.AgentRunStatusFailed) || run.Status == string(domain.AgentRunStatusSucceeded)
	}, 20*time.Second, 50*time.Millisecond, "resumed run settles")
	require.Equal(t, string(domain.AgentRunStatusSucceeded), run.Status)
}
