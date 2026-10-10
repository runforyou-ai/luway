//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// processSimulation 按运行时的规则在内存中演进一次运行的完整过程，并按已写入的内容计算每个安全点的过程变化。
type processSimulation struct {
	blocks       []agentcontract.Block
	children     []agentcontract.ToolCall
	modelCallID  string
	savedBlocks  map[string]agentcontract.Block
	savedCalls   map[string]agentcontract.ToolCall
	replay       []processWrite // 按旧逻辑整体写入时依次执行的写入。
	stepSizes    []int          // 每个安全点携带的变化行数。
	fullSizes    []int          // 每个安全点按旧逻辑整体写入的行数。
	journal      einorun.Journal
	toolCallsNow time.Time
	revs         map[string]uint64 // 调用编号到运行时最近一次写入的修订号，每次变化加一。
}

// processWrite 是一次单独写入的工具调用，或一次安全点的完整过程。
type processWrite struct {
	call     *einorun.ToolCall
	blocks   []einorun.Block
	children []einorun.ToolCall
}

// record 把调用的当前内容转换为运行时的调用记录，修订号加一后写入。
func (s *processSimulation) record(call agentcontract.ToolCall) einorun.ToolCall {
	s.revs[call.ID]++
	converted := runCall(call)
	converted.Rev = s.revs[call.ID]
	return converted
}

// records 把内容块与子 Agent 调用转换为运行时的记录，调用沿用最近一次写入的修订号。
func (s *processSimulation) records(blocks []agentcontract.Block, children []agentcontract.ToolCall) ([]einorun.Block, []einorun.ToolCall) {
	converted := runBlocks(blocks)
	for i := range converted {
		if call := converted[i].Call; call != nil {
			call.Rev = s.revs[call.ID]
		}
	}
	calls := make([]einorun.ToolCall, len(children))
	for i, child := range children {
		calls[i] = runCall(child)
		calls[i].Rev = s.revs[child.ID]
	}
	return converted, calls
}

// beginModelCall 开始一次新的模型调用。
func (s *processSimulation) beginModelCall() {
	s.modelCallID = uuid.NewV7().String()
}

// addText 追加思考或正文块并返回块编号。
func (s *processSimulation) addText(kind domain.AgentRunBlockKind, text string) string {
	id := uuid.NewV7().String()
	s.blocks = append(s.blocks, agentcontract.Block{ID: id, ModelCallID: s.modelCallID, Kind: kind, Payload: agentcontract.BlockPayload{Text: text}})
	return id
}

// addCall 追加排队中的工具调用块并返回调用。
func (s *processSimulation) addCall(callID, name string) *agentcontract.ToolCall {
	call := &agentcontract.ToolCall{ID: uuid.NewV7().String(), ModelCallID: s.modelCallID, CallID: callID, Name: name,
		Source: domain.AgentToolSourceBuiltin, Replayable: true, Arguments: `{"text":"` + callID + `"}`, Status: domain.AgentToolCallQueued}
	s.blocks = append(s.blocks, agentcontract.Block{ID: uuid.NewV7().String(), ModelCallID: s.modelCallID, Kind: domain.AgentRunBlockToolCall,
		Payload: agentcontract.BlockPayload{ToolCall: call}})
	return call
}

// removeBlocks 移除满足条件的内容块。
func (s *processSimulation) removeBlocks(match func(agentcontract.Block) bool) {
	s.blocks = slices.DeleteFunc(s.blocks, match)
}

// snapshot 返回当前完整过程的副本，块位置按顺序编号。
func (s *processSimulation) snapshot() ([]agentcontract.Block, []agentcontract.ToolCall) {
	blocks := make([]agentcontract.Block, len(s.blocks))
	for index, block := range s.blocks {
		block.Position = int64(index + 1)
		if call := block.Payload.ToolCall; call != nil {
			copied := *call
			block.Payload.ToolCall = &copied
		}
		blocks[index] = block
	}
	return blocks, slices.Clone(s.children)
}

// writeCall 像工具状态变化时一样单独写入一次工具调用。
func (s *processSimulation) writeCall(t *testing.T, ctx context.Context, call agentcontract.ToolCall) {
	t.Helper()
	record := s.record(call)
	require.NoError(t, s.journal.SaveToolCall(ctx, record))
	s.savedCalls[call.ID] = call
	s.replay = append(s.replay, processWrite{call: &record})
}

// start 把工具调用标记为执行中并单独写入。
func (s *processSimulation) start(t *testing.T, ctx context.Context, call *agentcontract.ToolCall) {
	t.Helper()
	call.Status, call.StartedAt = domain.AgentToolCallRunning, &s.toolCallsNow
	s.writeCall(t, ctx, *call)
}

// finish 记录工具调用的结果并单独写入。
func (s *processSimulation) finish(t *testing.T, ctx context.Context, call *agentcontract.ToolCall) {
	t.Helper()
	result := "结果 " + call.CallID
	call.Status, call.Result, call.CompletedAt = domain.AgentToolCallSucceeded, &result, &s.toolCallsNow
	s.writeCall(t, ctx, *call)
}

// step 计算自上次保存以来的过程变化并作为安全点写入，记录本次变化与整体写入的行数。
func (s *processSimulation) step(t *testing.T, ctx context.Context) {
	t.Helper()
	blocks, children := s.snapshot()
	var changes einorun.Changes
	current := make(map[string]agentcontract.ToolCall)
	for _, block := range blocks {
		if call := block.Payload.ToolCall; call != nil {
			current[call.ID] = *call
			if saved, ok := s.savedCalls[call.ID]; !ok || !reflect.DeepEqual(saved, *call) {
				changes.Calls = append(changes.Calls, s.record(*call))
			}
		}
	}
	for _, call := range children {
		current[call.ID] = call
		if saved, ok := s.savedCalls[call.ID]; !ok || !reflect.DeepEqual(saved, call) {
			changes.Calls = append(changes.Calls, s.record(call))
		}
	}
	records, childRecords := s.records(blocks, children)
	for index, block := range blocks {
		saved, ok := s.savedBlocks[block.ID]
		if !ok || saved.Position != block.Position || saved.Payload.Text != block.Payload.Text {
			changes.Blocks = append(changes.Blocks, records[index])
		}
	}
	for id := range s.savedBlocks {
		if !slices.ContainsFunc(blocks, func(block agentcontract.Block) bool { return block.ID == id }) {
			changes.RemovedBlocks = append(changes.RemovedBlocks, id)
		}
	}
	for id := range s.savedCalls {
		if _, ok := current[id]; !ok {
			changes.RemovedCalls = append(changes.RemovedCalls, id)
		}
	}
	require.NoError(t, s.journal.SaveStep(ctx, einorun.Step{Changes: changes, State: []byte("state")}))
	s.savedBlocks, s.savedCalls = arr.KeyBy(blocks, func(block agentcontract.Block) string { return block.ID }), current
	s.replay = append(s.replay, processWrite{blocks: records, children: childRecords})
	s.stepSizes = append(s.stepSizes, len(changes.Blocks)+len(changes.Calls)+len(changes.RemovedBlocks)+len(changes.RemovedCalls))
	s.fullSizes = append(s.fullSizes, len(blocks)+len(current))
}

// requireMatchesFullRewrite 断言增量写入后读取的过程与按旧逻辑从空过程依次整体写入后读取的过程逐字段一致；旧逻辑在回滚的事务中执行。
func (s *processSimulation) requireMatchesFullRewrite(t *testing.T, ctx context.Context, db *bun.DB, run *servermodels.AgentRun) {
	t.Helper()
	gotBlocks, gotCalls, err := agentprocess.Load(ctx, db, run.WorkspaceID, run.ID)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.NewDelete().Model((*servermodels.AgentRunBlock)(nil)).Where("agent_run_id = ?", run.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = tx.NewDelete().Model((*servermodels.AgentToolCall)(nil)).Where("agent_run_id = ?", run.ID).Exec(ctx)
	require.NoError(t, err)
	for _, write := range s.replay {
		if write.call != nil {
			_, err := agentprocess.SaveCalls(ctx, tx, run, []einorun.ToolCall{*write.call})
			require.NoError(t, err)
			continue
		}
		_, err := agentprocess.SyncRecords(ctx, tx, run, write.blocks, write.children)
		require.NoError(t, err)
	}
	wantBlocks, wantCalls, err := agentprocess.Load(ctx, tx, run.WorkspaceID, run.ID)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(wantBlocks, gotBlocks, cmpopts.EquateEmpty()), "blocks differ from full rewrite (-want +got)")
	require.Empty(t, cmp.Diff(wantCalls, gotCalls, cmpopts.EquateEmpty()), "child calls differ from full rewrite (-want +got)")
	// 已写入的过程与运行时的完整过程一致。
	blocks, children := s.snapshot()
	require.Len(t, gotBlocks, len(blocks))
	require.Len(t, gotCalls, len(children))
	for index, block := range blocks {
		require.Equal(t, block.ID, gotBlocks[index].ID, "block %d", index)
		require.Equal(t, block.Position, gotBlocks[index].Position, "block %d", index)
	}
}

// testAgentRunIncrementalSafepoint 验证长运行的安全点只写入过程变化：顺序与并行工具调用、子 Agent 调用、补入新消息时移除未启动的工具、
// 作废终止工具调用与定稿时块顺序调整之后，从数据库读取的过程与按旧逻辑每步整体写入的结果逐字段一致，每步写入行数不随步数增长。
func testAgentRunIncrementalSafepoint(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	const sequential = 15
	var simulation *processSimulation
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		s := &processSimulation{journal: request.Journal, savedBlocks: map[string]agentcontract.Block{}, savedCalls: map[string]agentcontract.ToolCall{},
			toolCallsNow: time.Now().UTC().Truncate(time.Microsecond), revs: map[string]uint64{}}
		simulation = s
		// 顺序工具调用：每次模型输出一个思考块与一个调用，调用结束后保存安全点。
		for index := range sequential {
			s.beginModelCall()
			s.addText(domain.AgentRunBlockThinking, fmt.Sprintf("第 %d 步", index))
			call := s.addCall(fmt.Sprintf("s%d", index), "echo")
			s.step(t, ctx)
			s.start(t, ctx, call)
			s.finish(t, ctx, call)
			s.step(t, ctx)
		}
		s.requireMatchesFullRewrite(t, ctx, db, &run)

		// 并行工具调用乱序结束，各自结束后保存安全点；其中一个调用的结果构成依据。
		s.beginModelCall()
		s.addText(domain.AgentRunBlockContent, "并行查询")
		parallel := []*agentcontract.ToolCall{s.addCall("p1", "echo"), s.addCall("p2", "echo"), s.addCall("p3", "echo")}
		s.step(t, ctx)
		for _, call := range parallel {
			s.start(t, ctx, call)
		}
		for _, index := range []int{2, 0, 1} {
			s.finish(t, ctx, parallel[index])
			if index == 0 {
				parallel[index].Evidence = true
			}
			s.step(t, ctx)
		}
		s.requireMatchesFullRewrite(t, ctx, db, &run)

		// 委派调用中子 Agent 发起的调用单独写入，委派调用的当前活动随安全点写入。
		s.beginModelCall()
		delegation := s.addCall("d1", "Agent")
		s.step(t, ctx)
		s.start(t, ctx, delegation)
		child := agentcontract.ToolCall{ID: uuid.NewV7().String(), ParentID: delegation.ID, ModelCallID: delegation.ModelCallID, CallID: "k1", Name: "echo",
			Source: domain.AgentToolSourceBuiltin, Replayable: true, Arguments: `{}`, Status: domain.AgentToolCallRunning, StartedAt: &s.toolCallsNow}
		s.children = append(s.children, child)
		s.writeCall(t, ctx, child)
		delegation.Activity = "echo"
		s.step(t, ctx)
		result := "子任务结果"
		s.children[0].Status, s.children[0].Result, s.children[0].CompletedAt = domain.AgentToolCallSucceeded, &result, &s.toolCallsNow
		s.writeCall(t, ctx, s.children[0])
		delegation.Activity = ""
		s.finish(t, ctx, delegation)
		s.step(t, ctx)
		s.requireMatchesFullRewrite(t, ctx, db, &run)

		// 补入新消息时移除末尾未启动的工具，新的模型输出沿用空出的位置。
		s.beginModelCall()
		s.addText(domain.AgentRunBlockThinking, "准备两个查询")
		s.addCall("q1", "echo")
		s.addCall("q2", "echo")
		s.step(t, ctx)
		s.removeBlocks(func(block agentcontract.Block) bool {
			return block.Payload.ToolCall != nil && block.Payload.ToolCall.Status == domain.AgentToolCallQueued
		})
		s.beginModelCall()
		s.addText(domain.AgentRunBlockThinking, "重新规划")
		s.addCall("q3", "echo")
		s.step(t, ctx)
		s.requireMatchesFullRewrite(t, ctx, db, &run)

		// 抢占作废的终止工具调用连同调用记录移除。
		s.beginModelCall()
		s.addCall("ask", "ask_customer")
		s.step(t, ctx)
		s.removeBlocks(func(block agentcontract.Block) bool {
			return block.Payload.ToolCall != nil && block.Payload.ToolCall.CallID == "ask"
		})
		s.step(t, ctx)
		s.requireMatchesFullRewrite(t, ctx, db, &run)

		// 定稿与流式阶段的块顺序不一致时，已写入的块换到其他已写入块原有的位置。
		s.beginModelCall()
		first := s.addText(domain.AgentRunBlockThinking, "流式思考")
		second := s.addText(domain.AgentRunBlockContent, "流式说明")
		s.step(t, ctx)
		last := len(s.blocks) - 1
		s.blocks[last-1], s.blocks[last] = s.blocks[last], s.blocks[last-1]
		s.blocks[last].Payload.Text = "定稿思考"
		s.addCall("f1", "echo")
		s.step(t, ctx)
		require.Equal(t, second, s.blocks[last-1].ID)
		require.Equal(t, first, s.blocks[last].ID)
		s.requireMatchesFullRewrite(t, ctx, db, &run)

		blocks, children := s.records(s.snapshot())
		return agentruntime.RunResult{Content: "完成", EndSeq: 1, Blocks: blocks, Calls: children}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NotNil(t, simulation)
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
	// 每个安全点写入的行数有固定上限，整体写入的行数随步数增长。
	written, full := 0, 0
	for index, size := range simulation.stepSizes {
		require.LessOrEqual(t, size, 8, "step %d", index)
		written += size
		full += simulation.fullSizes[index]
	}
	require.Greater(t, slices.Max(simulation.fullSizes), 4*sequential)
	require.Less(t, written*5, full, "incremental rows %d, full rewrite rows %d", written, full)
	t.Logf("safepoints=%d incremental rows=%d full rewrite rows=%d", len(simulation.stepSizes), written, full)
}
