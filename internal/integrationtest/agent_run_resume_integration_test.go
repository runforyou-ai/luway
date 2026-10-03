//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// testAgentRunSuspendAndResume 验证运行挂起后保留过程与恢复状态，等待的调用有结果后改回排队，恢复执行读到已保存的状态与外部结果并正常收尾。
func testAgentRunSuspendAndResume(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertask.Runtime) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	modelCallID := uuid.NewV7().String()
	waiting := agentruntime.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "w1", Name: "await_result", Source: domain.AgentToolSourceBuiltin,
		SideEffects: true, Arguments: `{"question":"q"}`, Status: domain.AgentToolCallWaiting, StartedAt: &startedAt,
	}
	blocks := []agentruntime.Block{
		{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockThinking, Payload: agentruntime.BlockPayload{Text: "需要等待外部结果"}},
		{ID: uuid.NewV7().String(), Position: 2, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &waiting}},
	}
	suspending := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		if request.Resume != nil || request.Journal == nil {
			t.Errorf("first execution resume = %v, journal = %v", request.Resume, request.Journal)
		}
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		step := agentruntime.Step{Blocks: blocks, Usage: agentruntime.Usage{TotalTokens: 5}, State: []byte("state-1")}
		if err := request.Journal.SaveStep(ctx, step); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Usage: step.Usage, Blocks: blocks, Suspended: true}, nil
	}}
	if err := agentrunaction.NewExecuteAction(db, tasks, suspending, testModelInvoker(db), testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)
	var saved servermodels.AgentToolCall
	if err := db.NewSelect().Model(&saved).Where("atc.id = ?", waiting.ID).Scan(ctx); err != nil || saved.Status != string(domain.AgentToolCallWaiting) || saved.AgentRunID != run.ID {
		t.Fatalf("saved tool call = %+v, err = %v", saved, err)
	}
	// 挂起后原任务重试与最终失败都不改变运行，已保存的过程可以查看。
	unexpected := testAgentRuntime{run: func(context.Context, agentruntime.RunRequest, agentruntime.InputFeed) (agentruntime.RunResult, error) {
		t.Error("waiting run executed by a retried task")
		return agentruntime.RunResult{}, nil
	}}
	retried := agentrunaction.NewExecuteAction(db, tasks, unexpected, testModelInvoker(db), testAttachmentReader(db), nil, nil)
	if err := retried.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := retried.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("retried task failed")); err != nil {
		t.Fatal(err)
	}
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)
	waitingProcess, err := conversationaction.NewGetAgentRunProcessQuery(db).Execute(ctx, identity, run.ID)
	if err != nil || len(waitingProcess.Blocks) != 2 || waitingProcess.Blocks[1].Payload.ToolCall.Status != domain.AgentToolCallWaiting {
		t.Fatalf("waiting process = %+v, err = %v", waitingProcess, err)
	}
	// 仍有调用等待时不恢复。
	if err := resumeWaitingRun(ctx, db, tasks, run); err != nil {
		t.Fatal(err)
	}
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)

	// 外部结果送达后改回排队；运行时之后重复写入等待状态不覆盖已有结果。
	result := "外部结果 42"
	if _, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallSucceeded).Set("result = ?", result).Set("completed_at = now()").
		Where("id = ?", waiting.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := resumeWaitingRun(ctx, db, tasks, run); err != nil {
		t.Fatal(err)
	}
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusQueued)

	resuming := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, _ agentruntime.InputFeed) (agentruntime.RunResult, error) {
		resume := request.Resume
		if resume == nil || string(resume.State) != "state-1" || len(resume.Blocks) != 2 {
			t.Fatalf("resume = %+v", resume)
		}
		call := resume.Blocks[1].Payload.ToolCall
		if call == nil || call.ID != waiting.ID || call.Status != domain.AgentToolCallSucceeded || call.Result == nil || *call.Result != result {
			t.Fatalf("resumed tool call = %+v", call)
		}
		stale := *call
		stale.Status, stale.Result = domain.AgentToolCallWaiting, nil
		if err := request.Journal.SaveToolCall(ctx, stale); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Content: "完成：42", EndSeq: 1, Usage: agentruntime.Usage{TotalTokens: 9}, Blocks: resume.Blocks}, nil
	}}
	if err := agentrunaction.NewExecuteAction(db, tasks, resuming, testModelInvoker(db), testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
	process, err := conversationaction.NewGetAgentRunProcessQuery(db).Execute(ctx, identity, run.ID)
	if err != nil || len(process.Blocks) != 2 || process.Blocks[0].Payload.Text != "需要等待外部结果" {
		t.Fatalf("process = %+v, err = %v", process, err)
	}
	if call := process.Blocks[1].Payload.ToolCall; call == nil || call.Status != domain.AgentToolCallSucceeded || call.Result == nil || *call.Result != result {
		t.Fatalf("process tool call = %+v", call)
	}
}

// testStopWaitingAgentRun 验证停止挂起的运行时，等待中的工具调用一并记为已取消。
func testStopWaitingAgentRun(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertask.Runtime) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	modelCallID := uuid.NewV7().String()
	waiting := agentruntime.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "w1", Name: "await_result", Source: domain.AgentToolSourceBuiltin,
		SideEffects: true, Arguments: `{}`, Status: domain.AgentToolCallWaiting,
	}
	blocks := []agentruntime.Block{{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &waiting}}}
	suspending := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := request.Journal.SaveStep(ctx, agentruntime.Step{Blocks: blocks, State: []byte("state")}); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Blocks: blocks, Suspended: true}, nil
	}}
	executor := agentrunaction.NewExecuteAction(db, tasks, suspending, testModelInvoker(db), testAttachmentReader(db), nil, nil)
	if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if status, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID); err != nil || status != domain.AgentRunStatusCancelled {
		t.Fatalf("stop waiting run = %s, %v", status, err)
	}
	var saved servermodels.AgentToolCall
	if err := db.NewSelect().Model(&saved).Where("atc.id = ?", waiting.ID).Scan(ctx); err != nil || saved.Status != string(domain.AgentToolCallCancelled) {
		t.Fatalf("tool call after stop = %+v, err = %v", saved, err)
	}
}

// testFailedAgentRunCancelsToolCalls 验证运行失败时已写入但尚未结束的工具调用记为已取消。
func testFailedAgentRunCancelsToolCalls(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertask.Runtime) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	modelCallID := uuid.NewV7().String()
	running := agentruntime.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "r1", Name: "echo", Source: domain.AgentToolSourceBuiltin,
		Replayable: true, Arguments: `{}`, Status: domain.AgentToolCallRunning,
	}
	blocks := []agentruntime.Block{{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &running}}}
	failing := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := request.Journal.SaveStep(ctx, agentruntime.Step{Blocks: blocks, State: []byte("state")}); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{}, errors.New("model failed")
	}}
	_ = agentrunaction.NewExecuteAction(db, tasks, failing, testModelInvoker(db), testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID})
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusFailed)
	var saved servermodels.AgentToolCall
	if err := db.NewSelect().Model(&saved).Where("atc.id = ?", running.ID).Scan(ctx); err != nil || saved.Status != string(domain.AgentToolCallCancelled) {
		t.Fatalf("tool call after failure = %+v, err = %v", saved, err)
	}
}

// resumeWaitingRun 在独立事务中检查挂起运行是否可以恢复。
func resumeWaitingRun(ctx context.Context, db *bun.DB, tasks *servertask.Runtime, run servermodels.AgentRun) error {
	return realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		return agentrunaction.ResumeWaitingRun(ctx, tx, tasks, run.OrganizationID, run.ID)
	})
}

// assertAgentRunStatus 核对运行的当前状态。
func assertAgentRunStatus(t *testing.T, ctx context.Context, db *bun.DB, runID string, want domain.AgentRunStatus) {
	t.Helper()
	var run servermodels.AgentRun
	if err := db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != string(want) {
		t.Fatalf("agent run status = %s, want %s", run.Status, want)
	}
}
