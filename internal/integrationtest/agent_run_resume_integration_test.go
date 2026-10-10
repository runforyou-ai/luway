//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testAgentRunSuspendAndResume 验证运行挂起后保留过程与恢复状态，等待的调用有结果后改回排队，恢复执行读到已保存的状态与外部结果并正常收尾。
func testAgentRunSuspendAndResume(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	modelCallID := uuid.NewV7().String()
	waiting := agentcontract.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "w1", Name: "await_result", Source: domain.AgentToolSourceBuiltin,
		SideEffects: true, Arguments: `{"question":"q"}`, Status: domain.AgentToolCallWaiting, StartedAt: &startedAt,
	}
	blocks := []agentcontract.Block{
		{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockThinking, Payload: agentcontract.BlockPayload{Text: "需要等待外部结果"}},
		{ID: uuid.NewV7().String(), Position: 2, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &waiting}},
	}
	suspending := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		assert.Nil(t, request.Resume, "first execution resume")
		assert.NotNil(t, request.Journal, "first execution journal")
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		step := einorun.Step{Changes: blockChanges(blocks), Usage: llm.Usage{Input: 3, Output: 2, Total: 5}, State: []byte("state-1")}
		if err := request.Journal.SaveStep(ctx, step); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Usage: agentcontract.UsageFrom(step.Usage), Blocks: runBlocks(blocks), Suspended: true}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, suspending, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)
	var saved servermodels.AgentToolCall
	require.NoError(t, db.NewSelect().Model(&saved).Where("atc.id = ?", waiting.ID).Scan(ctx))
	require.Equal(t, string(domain.AgentToolCallWaiting), saved.Status)
	require.Equal(t, run.ID, saved.AgentRunID)
	// 挂起后原任务重试与最终失败都不改变运行，已保存的过程可以查看。
	unexpected := testAgentRuntime{run: func(context.Context, agentruntime.RunRequest, einorun.Feed) (agentruntime.RunResult, error) {
		t.Error("waiting run executed by a retried task")
		return agentruntime.RunResult{}, nil
	}}
	retried := newTestAgentRun(db, tasks, unexpected, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	require.NoError(t, retried.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, retried.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("retried task failed")))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)
	waitingProcess, err := processquery.NewGetRunProcessQuery(db).Execute(ctx, identity, run.ID)
	require.NoError(t, err)
	require.Len(t, waitingProcess.Blocks, 2)
	require.Equal(t, domain.AgentToolCallWaiting, waitingProcess.Blocks[1].Payload.ToolCall.Status)
	// 仍有调用等待时不恢复。
	require.NoError(t, resumeWaitingRun(ctx, db, tasks, run))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)

	// 外部结果送达后改回排队；运行时之后重复写入等待状态不覆盖已有结果。
	result := "外部结果 42"
	_, err = db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallSucceeded).Set("result = ?", result).Set("completed_at = now()").
		Where("id = ?", waiting.ID).Exec(ctx)
	require.NoError(t, err)
	// 恢复投递的任务从第一次尝试开始，原任务的执行尝试与执行实例不得保留。
	_, err = db.NewUpdate().Model((*servermodels.AgentRun)(nil)).
		Set("task_attempt = 3").Set("task_instance_id = ?", uuid.NewV7().String()).Where("id = ?", run.ID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, resumeWaitingRun(ctx, db, tasks, run))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusQueued)
	var resumed servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&resumed).Where("agr.id = ?", run.ID).Scan(ctx))
	require.Zero(t, resumed.TaskAttempt, "resumed task attempt")
	require.Nil(t, resumed.TaskInstanceID, "resumed task instance")
	require.NotNil(t, resumed.TaskRunID, "resumed task")

	resuming := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, _ einorun.Feed) (agentruntime.RunResult, error) {
		resume := request.Resume
		require.NotNil(t, resume)
		require.Equal(t, "state-1", string(resume.State))
		require.Len(t, resume.Blocks, 2)
		call := resume.Blocks[1].Call
		require.NotNil(t, call)
		require.Equal(t, waiting.ID, call.ID)
		require.Equal(t, einorun.StatusSucceeded, call.Status)
		require.NotNil(t, call.Result)
		require.Equal(t, result, *call.Result)
		stale := *call
		stale.Rev++
		stale.Status, stale.Result = einorun.StatusWaiting, nil
		if err := request.Journal.SaveToolCall(ctx, stale); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Content: "完成：42", EndSeq: 1, Usage: agentcontract.Usage{TotalTokens: 9}, Blocks: resume.Blocks}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, resuming, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
	process, err := processquery.NewGetRunProcessQuery(db).Execute(ctx, identity, run.ID)
	require.NoError(t, err)
	require.Len(t, process.Blocks, 2)
	require.Equal(t, "需要等待外部结果", process.Blocks[0].Payload.Text)
	call := process.Blocks[1].Payload.ToolCall
	require.NotNil(t, call)
	require.Equal(t, domain.AgentToolCallSucceeded, call.Status)
	require.NotNil(t, call.Result)
	require.Equal(t, result, *call.Result)
}

// testStopWaitingAgentRun 验证停止挂起的运行时，等待中的工具调用一并记为已取消。
func testStopWaitingAgentRun(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	modelCallID := uuid.NewV7().String()
	waiting := agentcontract.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "w1", Name: "await_result", Source: domain.AgentToolSourceBuiltin,
		SideEffects: true, Arguments: `{}`, Status: domain.AgentToolCallWaiting,
	}
	blocks := []agentcontract.Block{{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &waiting}}}
	suspending := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := request.Journal.SaveStep(ctx, einorun.Step{Changes: blockChanges(blocks), State: []byte("state")}); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Blocks: runBlocks(blocks), Suspended: true}, nil
	}}
	executor := newTestAgentRun(db, tasks, suspending, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	status, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.AgentRunStatusCancelled, status, "stop waiting run")
	var saved servermodels.AgentToolCall
	require.NoError(t, db.NewSelect().Model(&saved).Where("atc.id = ?", waiting.ID).Scan(ctx))
	require.Equal(t, string(domain.AgentToolCallCancelled), saved.Status, "tool call after stop")
}

// testFailedAgentRunCancelsToolCalls 验证运行失败时已写入但尚未结束的工具调用按是否已开始结算：未开始的取消，已开始的中断。
func testFailedAgentRunCancelsToolCalls(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	modelCallID := uuid.NewV7().String()
	running := agentcontract.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "r1", Name: "echo", Source: domain.AgentToolSourceBuiltin,
		Replayable: true, Arguments: `{}`, Status: domain.AgentToolCallRunning,
	}
	queued := agentcontract.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "q1", Name: "echo", Source: domain.AgentToolSourceBuiltin,
		Replayable: true, Arguments: `{}`, Status: domain.AgentToolCallQueued,
	}
	blocks := []agentcontract.Block{
		{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &running}},
		{ID: uuid.NewV7().String(), Position: 2, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &queued}},
	}
	failing := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := request.Journal.SaveStep(ctx, einorun.Step{Changes: blockChanges(blocks), State: []byte("state")}); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{}, errors.New("model failed")
	}}
	_ = newTestAgentRun(db, tasks, failing, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID})
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusFailed)
	for id, want := range map[string]domain.AgentToolCallStatus{running.ID: domain.AgentToolCallInterrupted, queued.ID: domain.AgentToolCallCancelled} {
		var saved servermodels.AgentToolCall
		require.NoError(t, db.NewSelect().Model(&saved).Where("atc.id = ?", id).Scan(ctx))
		require.Equal(t, string(want), saved.Status, "tool call after failure")
	}
}

// blockChanges 返回把给定内容块作为新增内容写入的过程变化，工具调用块的调用一并写入。
func blockChanges(blocks []agentcontract.Block) einorun.Changes {
	changes := einorun.Changes{Blocks: runBlocks(blocks)}
	for _, block := range changes.Blocks {
		if block.Call != nil {
			changes.Calls = append(changes.Calls, *block.Call)
		}
	}
	return changes
}

// resumeWaitingRun 在独立事务中检查挂起运行是否可以恢复。
func resumeWaitingRun(ctx context.Context, db *bun.DB, tasks *servertest.Tasks, run servermodels.AgentRun) error {
	return realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		return agentrunaction.ResumeWaitingRun(ctx, tx, tasks, run.WorkspaceID, run.ID)
	})
}

// assertAgentRunStatus 核对运行的当前状态。
func assertAgentRunStatus(t *testing.T, ctx context.Context, db *bun.DB, runID string, want domain.AgentRunStatus) {
	t.Helper()
	var run servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(ctx))
	require.Equal(t, string(want), run.Status, "agent run status")
}

// testAgentRunEndedWithoutNotice 验证运行结束通知未送达时，推理过程的下一次写入发现运行已结束即停止：工具调用与恢复状态不再写入，已产生的过程内容在收尾时保留。
func testAgentRunEndedWithoutNotice(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	modelCallID := uuid.NewV7().String()
	blocks := []agentcontract.Block{{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockThinking, Payload: agentcontract.BlockPayload{Text: "先查询订单"}}}
	call := agentcontract.ToolCall{ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "c1", Name: "lookup", Source: domain.AgentToolSourceBuiltin, Arguments: "{}", Status: domain.AgentToolCallQueued}
	var toolErr, stepErr error
	ending := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := request.Journal.SaveStep(ctx, einorun.Step{Changes: blockChanges(blocks), State: []byte("state-1")}); err != nil {
			return agentruntime.RunResult{}, err
		}
		// 运行在其他实例被结束，结束通知没有送达本实例。
		if _, err := db.NewUpdate().Model((*servermodels.AgentRun)(nil)).
			Set("status = ?", domain.AgentRunStatusCancelled).Set("completed_at = now()").
			Where("id = ?", run.ID).Exec(ctx); err != nil {
			return agentruntime.RunResult{}, err
		}
		toolErr = request.Journal.SaveToolCall(ctx, runCall(call))
		stepErr = request.Journal.SaveStep(ctx, einorun.Step{Changes: blockChanges(blocks), State: []byte("state-2")})
		return agentruntime.RunResult{Blocks: runBlocks(blocks)}, stepErr
	}}
	require.NoError(t, newTestAgentRun(db, tasks, ending, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.Error(t, toolErr, "journal tool write after run ended")
	require.Error(t, stepErr, "journal step write after run ended")
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusCancelled)
	var state []byte
	require.NoError(t, db.NewSelect().Model((*servermodels.AgentRun)(nil)).Column("state").Where("id = ?", run.ID).Scan(ctx, &state))
	require.Equal(t, "state-1", string(state))
	count, err := db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).Where("atc.id = ?", call.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "tool calls written after run ended")
	process, err := processquery.NewGetRunProcessQuery(db).Execute(ctx, identity, run.ID)
	require.NoError(t, err)
	require.Len(t, process.Blocks, 1)
	require.Equal(t, "先查询订单", process.Blocks[0].Payload.Text)
}

// testAgentRunReviewNotice 验证同批中有等待外部结果的调用时，恢复写入待核对的调用后立即挂起，负责人仍收到待处理变更通知；
// 外部结果送达后再次恢复，待核对的调用保持不变。
func testAgentRunReviewNotice(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	_, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("responsible_user_id = ?", identity.User.ID).
		Where("workspace_id = ? AND identity_id = ?", identity.Workspace.ID, agentIdentityID).Exec(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("responsible_user_id = NULL").
			Where("workspace_id = ? AND identity_id = ?", identity.Workspace.ID, agentIdentityID).Exec(context.Background())
		assert.NoError(t, err)
	})
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	feed := startRealtimeFeed(t, identity.Workspace.ID)
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	modelCallID := uuid.NewV7().String()
	waiting := agentcontract.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "w1", Name: "await_result", Source: domain.AgentToolSourceBuiltin,
		SideEffects: true, Arguments: `{}`, Status: domain.AgentToolCallWaiting, StartedAt: &startedAt,
	}
	effect := agentcontract.ToolCall{
		ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: "e1", Name: "remote", Source: domain.AgentToolSourceMCP,
		SideEffects: true, Arguments: `{}`, Status: domain.AgentToolCallRunning, StartedAt: &startedAt,
	}
	blocks := []agentcontract.Block{
		{ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &waiting}},
		{ID: uuid.NewV7().String(), Position: 2, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &effect}},
	}
	// 安全点写入执行中的副作用调用后中断；恢复时把它写为待核对，等待的调用仍未结束，运行不经安全点立即挂起。
	suspending := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		if err := request.Journal.SaveStep(ctx, einorun.Step{Changes: blockChanges(blocks), State: []byte("state")}); err != nil {
			return agentruntime.RunResult{}, err
		}
		status, result := agentcontract.InterruptedStatus(effect.Replayable, effect.SideEffects)
		reviewed := effect
		reviewed.Status, reviewed.Result = status, &result
		record := runCall(reviewed)
		record.Rev = 2
		if err := request.Journal.SaveToolCall(ctx, record); err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Blocks: runBlocks(blocks), Suspended: true}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, suspending, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)
	// 负责人收到待处理变更通知，期间忽略会话变更等其他通知。
	subject := realtime.Topic(identity.Workspace.ID, realtime.AudienceUser, identity.User.ID)
	for {
		notification, err := feed.next(t)
		require.NoError(t, err, "wait for tool decisions notice")
		if notification.Subject == subject && notification.Kind == string(realtime.KindToolDecisionsChanged) {
			break
		}
	}

	// 外部结果送达后再次恢复，待核对的调用保持不变。
	_, err = db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("status = ?", domain.AgentToolCallSucceeded).Set("result = ?", "外部结果").Set("completed_at = now()").
		Where("id = ?", waiting.ID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, resumeWaitingRun(ctx, db, tasks, run))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusQueued)
	resuming := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, _ einorun.Feed) (agentruntime.RunResult, error) {
		require.NotNil(t, request.Resume)
		require.Len(t, request.Resume.Blocks, 2)
		require.Equal(t, einorun.StatusNeedsReview, request.Resume.Blocks[1].Call.Status)
		return agentruntime.RunResult{Content: "完成", EndSeq: 1, Blocks: request.Resume.Blocks}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, resuming, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
	var saved servermodels.AgentToolCall
	require.NoError(t, db.NewSelect().Model(&saved).Where("atc.id = ?", effect.ID).Scan(ctx))
	require.Equal(t, string(domain.AgentToolCallNeedsReview), saved.Status)
}
