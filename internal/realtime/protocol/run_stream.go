package protocol

import (
	"strconv"

	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

// runStreamOperationKinds 把运行流的操作类型对应到事件契约中的操作类型。
var runStreamOperationKinds = map[stream.OpKind]RunStreamOperationKind{
	stream.OpUpsertBlock:     RunStreamUpsertBlock,
	stream.OpAppendText:      RunStreamAppendBlockText,
	stream.OpRemoveBlocks:    RunStreamRemoveBlocks,
	stream.OpAppendCandidate: RunStreamAppendCandidate,
	stream.OpClearCandidate:  RunStreamClearCandidate,
	stream.OpSetPlan:         RunStreamSetPlan,
}

// RunStreamSnapshotView 投影一次执行尝试的完整快照，streamID 是执行尝试的流编号。
func RunStreamSnapshotView(runID string, attempt int, snapshot stream.Snapshot) appservice.RunStreamSnapshot {
	return appservice.RunStreamSnapshot{RunID: runID, StreamID: snapshot.Stream, Attempt: attempt, Sequence: strconv.FormatInt(snapshot.Sequence, 10), CandidateContent: snapshot.Candidate, Plan: arr.OrEmpty(RunStreamPlan(snapshot.Plan)), Blocks: arr.OrEmpty(arr.Map(snapshot.Blocks, runStreamBlock))}
}

// RunStreamDeltaFrame 把一次执行尝试的运行流增量转换为事件契约中的增量。
func RunStreamDeltaFrame(runID string, attempt int, delta stream.Delta) RunStreamDelta {
	operations := arr.OrEmpty(arr.Map(delta.Operations, func(operation stream.Operation) RunStreamOperation {
		return RunStreamOperation{
			Kind:     runStreamOperationKinds[operation.Kind],
			BlockID:  operation.BlockID,
			BlockIDs: operation.BlockIDs,
			Text:     operation.Text,
			Plan:     RunStreamPlan(operation.Plan),
			Block:    support.MapPtr(operation.Block, runStreamBlock),
		}
	}))
	return RunStreamDelta{RunID: runID, StreamID: delta.Stream, Attempt: attempt,
		BaseSequence: delta.Base, Sequence: delta.Sequence, Operations: operations}
}

// runStreamBlock 把运行流内容块转换为事件契约中的展示块。
func runStreamBlock(block stream.Block) RunStreamBlock {
	view := RunStreamBlock{ID: block.ID, Position: block.Position, Kind: domain.AgentRunBlockKind(block.Kind), Text: block.Text}
	if call := block.Call; call != nil {
		view.ToolCall = &RunStreamToolCall{Name: call.Name, Status: domain.AgentToolCallStatus(call.Status), StartedAt: call.StartedAt, CompletedAt: call.CompletedAt, Description: call.Description, Activity: call.Activity}
	}
	return view
}

// RunStreamPlan 把运行任务清单转换为事件契约中的任务清单。
func RunStreamPlan(plan []stream.PlanTask) []RunStreamPlanTask {
	return arr.Map(plan, func(task stream.PlanTask) RunStreamPlanTask {
		return RunStreamPlanTask{ID: task.ID, Subject: task.Subject, ActiveForm: task.ActiveForm, Status: domain.AgentPlanTaskStatus(task.Status)}
	})
}
