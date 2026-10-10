//go:build server

package agentprocess

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// CallChange 是一次合并写入前后的调用记录，调用新建时 Before 为空。
type CallChange struct {
	Before *einorun.ToolCall
	After  einorun.ToolCall
}

// NewlySettledAs 判断调用经本次写入首次进入 status。
func (c CallChange) NewlySettledAs(status einorun.CallStatus) bool {
	return c.After.Status == status && (c.Before == nil || c.Before.Status != status)
}

// NewlyHandedOver 判断调用经本次写入首次以 handover 交出。
func (c CallChange) NewlyHandedOver(handover einorun.Handover) bool {
	return c.After.Handover == handover && (c.Before == nil || c.Before.Handover != handover)
}

// Submitted 判断工具调用记录是否提交给处理人：确认或批准后由服务端在运行外执行，结果以事件送达 AI 员工。
func Submitted(row *servermodels.AgentToolCall) bool {
	return row.Handover != nil && einorun.Handover(*row.Handover) == einorun.HandoverSubmitted
}

// Paused 判断调用是否暂停运行等待确认：由运行时推进且等待处理人的决定。
func Paused(call einorun.ToolCall) bool {
	return call.Handover == einorun.HandoverNone && call.Status == einorun.StatusAwaitingDecision
}

// CallRecord 把工具调用记录转换为运行时的调用记录。
func CallRecord(row servermodels.AgentToolCall) einorun.ToolCall {
	call := einorun.ToolCall{
		ID: row.ID, ParentID: support.Deref(row.ParentID), ModelCallID: row.ModelCallID, CallID: row.ProviderCallID,
		Name: row.Name, Arguments: row.Arguments, Rev: row.Rev, Result: row.Result, Error: row.Error,
		Status: einorun.CallStatus(row.Status), StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
		Replayable: row.Replayable, SideEffects: row.SideEffects, Handover: einorun.Handover(support.Deref(row.Handover)),
		Payload: row.Payload, Notes: row.Notes,
	}
	if len(row.Media) > 0 {
		_ = json.Unmarshal(row.Media, &call.Media)
	}
	if len(row.Completion) > 0 {
		call.Completion = &einorun.CallCompletion{}
		_ = json.Unmarshal(row.Completion, call.Completion)
	}
	if len(row.Decision) > 0 {
		call.Decision = &einorun.CallDecision{}
		_ = json.Unmarshal(row.Decision, call.Decision)
	}
	return call
}

// resumeRecord 把工具调用记录转换为恢复时交回运行时的调用记录：提交确认或审批的调用与交给本机 Agent 的调用以交给模型的回执为结果，
// 电脑执行的调用以派发的操作与上报的结果为载荷。
func resumeRecord(row servermodels.AgentToolCall) einorun.ToolCall {
	call := CallRecord(row)
	switch {
	case call.Handover == einorun.HandoverSubmitted && row.Intervention != nil:
		call.Result = new(agentcontract.SubmittedResult(domain.ToolIntervention(*row.Intervention)))
	case row.LocalAgentSessionID != nil && row.Operation != nil:
		call.Result = new(agentcontract.LocalAgentSubmitted(row.Operation.Operation.LocalAgent))
	case row.ComputerID != nil && row.Operation != nil:
		if payload, err := json.Marshal(row.Operation); err == nil {
			call.Payload = payload
		}
	}
	return call
}

// LoadRecords 按位置顺序读取运行的内容块与子 Agent 调用，供运行恢复使用：调用投影宿主的最新状态，本机 Agent 的权限请求不属于运行的过程。
func LoadRecords(ctx context.Context, db bun.IDB, workspaceID, runID string) ([]einorun.Block, []einorun.ToolCall, error) {
	var rows []servermodels.AgentRunBlock
	if err := db.NewSelect().Model(&rows).
		Where("arb.workspace_id = ? AND arb.agent_run_id = ?", workspaceID, runID).
		OrderExpr("arb.position").Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("load agent run blocks: %w", err)
	}
	var calls []servermodels.AgentToolCall
	if err := db.NewSelect().Model(&calls).
		Where("atc.workspace_id = ? AND atc.agent_run_id = ?", workspaceID, runID).
		Where("atc.source <> ?", domain.AgentToolSourceLocalAgent).
		OrderExpr("atc.id").Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("load agent tool calls: %w", err)
	}
	byID := make(map[string]einorun.ToolCall, len(calls))
	children := make([]einorun.ToolCall, 0)
	for _, row := range calls {
		call := resumeRecord(row)
		byID[row.ID] = call
		if row.ParentID != nil {
			children = append(children, call)
		}
	}
	blocks := make([]einorun.Block, 0, len(rows))
	for _, row := range rows {
		block := einorun.Block{ID: row.ID, Position: row.Position, ModelCallID: row.ModelCallID, Kind: einorun.BlockKind(row.Kind), Text: support.Deref(row.Content)}
		if row.ToolCallID != nil {
			call, ok := byID[*row.ToolCallID]
			if !ok {
				return nil, nil, fmt.Errorf("agent run block %s references a missing tool call", row.ID)
			}
			block.Call = &call
		}
		blocks = append(blocks, block)
	}
	return blocks, children, nil
}

// SaveCalls 按运行时的合并规则写入调用快照并返回每次写入前后的记录：已有记录先在事务中加锁读出，以 einorun.MergeCall 合并后整行写回；
// 电脑派发、领取、裁决与本机 Agent 会话等宿主维护的列保持不变，提交确认或审批的调用首次交出时记下介入方式与批准后派发的电脑和操作。
// 同一编号出现多次时取修订号最大的一次。调用方必须处于事务内。
func SaveCalls(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, calls []einorun.ToolCall) ([]CallChange, error) {
	latest := make(map[string]einorun.ToolCall, len(calls))
	ids := make([]string, 0, len(calls))
	for _, call := range calls {
		if err := call.Validate(); err != nil {
			return nil, err
		}
		previous, seen := latest[call.ID]
		if !seen {
			ids = append(ids, call.ID)
		}
		if !seen || call.Rev >= previous.Rev {
			latest[call.ID] = call
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	slices.Sort(ids)
	var stored []servermodels.AgentToolCall
	if err := db.NewSelect().Model(&stored).
		Where("atc.workspace_id = ? AND atc.agent_run_id = ? AND atc.id IN (?)", run.WorkspaceID, run.ID, bun.List(ids)).
		OrderExpr("atc.id").For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("lock agent tool calls: %w", err)
	}
	existing := make(map[string]einorun.ToolCall, len(stored))
	for _, row := range stored {
		existing[row.ID] = CallRecord(row)
	}
	changes := make([]CallChange, 0, len(ids))
	rows := make([]servermodels.AgentToolCall, 0, len(ids))
	for _, id := range ids {
		change := CallChange{}
		if before, ok := existing[id]; ok {
			change.Before = &before
		}
		change.After = einorun.MergeCall(change.Before, latest[id])
		changes = append(changes, change)
		rows = append(rows, callRow(run, change.After))
	}
	if _, err := db.NewInsert().Model(&rows).
		On("CONFLICT (id) DO UPDATE").
		Set("rev = EXCLUDED.rev").
		Set("parent_id = EXCLUDED.parent_id").
		Set("model_call_id = EXCLUDED.model_call_id").
		Set("provider_call_id = EXCLUDED.provider_call_id").
		Set("name = EXCLUDED.name").
		Set("arguments = EXCLUDED.arguments").
		Set("replayable = EXCLUDED.replayable").
		Set("side_effects = EXCLUDED.side_effects").
		Set("notes = EXCLUDED.notes").
		Set("source = EXCLUDED.source").
		Set("mcp_server = EXCLUDED.mcp_server").
		Set("business_system_id = EXCLUDED.business_system_id").
		Set("business_system_name = EXCLUDED.business_system_name").
		Set("level = EXCLUDED.level").
		Set("bound_arguments = EXCLUDED.bound_arguments").
		Set("evidence = EXCLUDED.evidence").
		Set("status = EXCLUDED.status").
		Set("result = EXCLUDED.result").
		Set("error = EXCLUDED.error").
		Set("media = EXCLUDED.media").
		Set("started_at = EXCLUDED.started_at").
		Set("completed_at = EXCLUDED.completed_at").
		Set("completion = EXCLUDED.completion").
		Set("handover = EXCLUDED.handover").
		Set("payload = EXCLUDED.payload").
		Set("intervention = COALESCE(atc.intervention, EXCLUDED.intervention)").
		Set("computer_id = COALESCE(atc.computer_id, EXCLUDED.computer_id)").
		Set("operation = COALESCE(atc.operation, EXCLUDED.operation)").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("save agent tool calls: %w", err)
	}
	return changes, nil
}

// callRow 把运行时的调用记录转换为工具调用记录：业务注解写入同名列，提交确认或审批的载荷写入介入方式与批准后派发的电脑和操作。
func callRow(run *servermodels.AgentRun, call einorun.ToolCall) servermodels.AgentToolCall {
	notes := call.Notes
	row := servermodels.AgentToolCall{
		ID: call.ID, WorkspaceID: run.WorkspaceID, AgentRunID: run.ID, ParentID: support.NilIfZero(call.ParentID),
		ModelCallID: call.ModelCallID, ProviderCallID: call.CallID, Name: call.Name, Arguments: str.Remove(call.Arguments, "\x00"),
		Replayable: call.Replayable, SideEffects: call.SideEffects, Status: string(call.Status), Result: call.Result, Error: call.Error,
		StartedAt: call.StartedAt, CompletedAt: call.CompletedAt, Rev: call.Rev, Handover: support.NilIfZero(string(call.Handover)),
		Payload: call.Payload, Notes: notes,
		Source:    cmp.Or(notes[agentcontract.NoteSource], string(domain.AgentToolSourceBuiltin)),
		MCPServer: support.NilIfZero(notes[agentcontract.NoteMCPServer]), Level: support.NilIfZero(notes[agentcontract.NoteLevel]),
		Evidence: notes[agentcontract.NoteEvidence] == "true",
	}
	if id := notes[agentcontract.NoteBusinessSystemID]; id != "" {
		row.BusinessSystemID, row.BusinessSystemName = &id, new(notes[agentcontract.NoteBusinessSystemName])
	}
	if bound := notes[agentcontract.NoteBoundArguments]; bound != "" {
		_ = json.Unmarshal([]byte(bound), &row.BoundArguments)
	}
	if len(call.Media) > 0 {
		row.Media, _ = json.Marshal(call.Media)
	}
	if call.Completion != nil {
		row.Completion, _ = json.Marshal(call.Completion)
	}
	// 提交确认或审批与暂停等待确认的调用记下介入方式，提交的电脑工具调用记下批准后派发的电脑与操作。
	if call.Handover == einorun.HandoverSubmitted || Paused(call) {
		var submission agentcontract.Submission
		if json.Unmarshal(call.Payload, &submission) == nil && submission.Intervention != "" {
			row.Intervention = new(string(submission.Intervention))
			if target := submission.Target; target != nil {
				row.ComputerID, row.Operation = &target.ComputerID, &domain.ComputerCall{Operation: target.Operation}
			}
		}
	}
	return row
}

// blockRecordRows 把运行时的内容块转换为内容块记录，工具调用块只记录对应的工具调用编号。
func blockRecordRows(run *servermodels.AgentRun, blocks []einorun.Block) []servermodels.AgentRunBlock {
	return arr.Map(blocks, func(block einorun.Block) servermodels.AgentRunBlock {
		row := servermodels.AgentRunBlock{ID: block.ID, WorkspaceID: run.WorkspaceID, AgentRunID: run.ID,
			Position: block.Position, ModelCallID: block.ModelCallID, Kind: string(block.Kind)}
		if block.Call != nil {
			row.ToolCallID = &block.Call.ID
		} else {
			row.Content = new(str.Remove(block.Text, "\x00"))
		}
		return row
	})
}

// ApplyChanges 按安全点的过程变化增量更新运行已保存的过程：删除移除的内容块与工具调用，按 SaveCalls 合并写入变化的调用，再写入变化的内容块；
// 变化的内容块先把已有位置取负腾开再写入。本机 Agent 的权限请求保持不变。调用方必须处于事务内。
func ApplyChanges(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, changes einorun.Changes) ([]CallChange, error) {
	if len(changes.RemovedBlocks) > 0 {
		if _, err := db.NewDelete().Model((*servermodels.AgentRunBlock)(nil)).
			Where("workspace_id = ? AND agent_run_id = ? AND id IN (?)", run.WorkspaceID, run.ID, bun.List(changes.RemovedBlocks)).
			Exec(ctx); err != nil {
			return nil, fmt.Errorf("delete removed agent run blocks: %w", err)
		}
	}
	if len(changes.RemovedCalls) > 0 {
		if _, err := db.NewDelete().Model((*servermodels.AgentToolCall)(nil)).
			Where("workspace_id = ? AND agent_run_id = ? AND source <> ? AND id IN (?)", run.WorkspaceID, run.ID, domain.AgentToolSourceLocalAgent, bun.List(changes.RemovedCalls)).
			Exec(ctx); err != nil {
			return nil, fmt.Errorf("delete removed agent tool calls: %w", err)
		}
	}
	saved, err := SaveCalls(ctx, db, run, changes.Calls)
	if err != nil {
		return nil, err
	}
	rows := blockRecordRows(run, changes.Blocks)
	if len(rows) == 0 {
		return saved, nil
	}
	ids := arr.Map(rows, func(row servermodels.AgentRunBlock) string { return row.ID })
	// 位置唯一，变化的块可能换到其他变化块原有的位置，先把这些块已有的位置取负腾开；只追加新块时不改动已有行。
	if _, err := db.NewUpdate().Model((*servermodels.AgentRunBlock)(nil)).
		Set("position = -position").
		Where("workspace_id = ? AND agent_run_id = ? AND id IN (?)", run.WorkspaceID, run.ID, bun.List(ids)).Exec(ctx); err != nil {
		return nil, fmt.Errorf("release changed agent run block positions: %w", err)
	}
	return saved, upsertBlocks(ctx, db, rows)
}

// SyncRecords 以给定的内容块与子 Agent 调用替换运行已保存的过程：删除给定集合以外的内容块与工具调用，调用按 SaveCalls 合并写入；
// 本机 Agent 的权限请求保持不变。调用方必须处于事务内。
func SyncRecords(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, blocks []einorun.Block, children []einorun.ToolCall) ([]CallChange, error) {
	calls := append(slices.Clone(children), arr.FilterMap(blocks, func(block einorun.Block) (einorun.ToolCall, bool) {
		return support.Deref(block.Call), block.Call != nil
	})...)
	rows := blockRecordRows(run, blocks)
	callIDs := arr.Map(calls, func(call einorun.ToolCall) string { return call.ID })
	blockIDs := arr.Map(rows, func(row servermodels.AgentRunBlock) string { return row.ID })
	deleteBlocks := db.NewDelete().Model((*servermodels.AgentRunBlock)(nil)).
		Where("workspace_id = ? AND agent_run_id = ?", run.WorkspaceID, run.ID)
	if len(blockIDs) > 0 {
		deleteBlocks = deleteBlocks.Where("id NOT IN (?)", bun.List(blockIDs))
	}
	if _, err := deleteBlocks.Exec(ctx); err != nil {
		return nil, fmt.Errorf("delete stale agent run blocks: %w", err)
	}
	deleteCalls := db.NewDelete().Model((*servermodels.AgentToolCall)(nil)).
		Where("workspace_id = ? AND agent_run_id = ? AND source <> ?", run.WorkspaceID, run.ID, domain.AgentToolSourceLocalAgent)
	if len(callIDs) > 0 {
		deleteCalls = deleteCalls.Where("id NOT IN (?)", bun.List(callIDs))
	}
	if _, err := deleteCalls.Exec(ctx); err != nil {
		return nil, fmt.Errorf("delete stale agent tool calls: %w", err)
	}
	saved, err := SaveCalls(ctx, db, run, calls)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return saved, nil
	}
	// 位置唯一，先把已有位置取负腾开，再写入本次位置。
	if _, err := db.NewUpdate().Model((*servermodels.AgentRunBlock)(nil)).
		Set("position = -position").
		Where("workspace_id = ? AND agent_run_id = ?", run.WorkspaceID, run.ID).Exec(ctx); err != nil {
		return nil, fmt.Errorf("release agent run block positions: %w", err)
	}
	return saved, upsertBlocks(ctx, db, rows)
}

// CallView 把运行时的调用记录转换为业务侧展示的工具调用，业务注解取自记录的注解。
func CallView(call einorun.ToolCall) agentcontract.ToolCall {
	return ToolCall(callRow(&servermodels.AgentRun{}, call))
}

// BlockViews 把运行时的内容块转换为业务侧展示的内容块。
func BlockViews(blocks []einorun.Block) []agentcontract.Block {
	return arr.Map(blocks, func(block einorun.Block) agentcontract.Block {
		view := agentcontract.Block{ID: block.ID, Position: block.Position, ModelCallID: block.ModelCallID, Kind: domain.AgentRunBlockKind(block.Kind)}
		view.Payload.Text = block.Text
		if block.Call != nil {
			view.Payload.ToolCall = new(CallView(*block.Call))
		}
		return view
	})
}
