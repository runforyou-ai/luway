package protocol

import (
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime/runstream"
)

// RunStreamSnapshotParts 把运行流快照按文本预算拆分为分片事件，每个分片至少包含一个内容块。
// 候选正文与任务清单整段放在首个分片，候选正文计入该分片预算，其长度由模型单次最大输出约束。
func RunStreamSnapshotParts(snapshot runstream.Snapshot, budget int) []RunStreamSnapshot {
	header := RunStreamSnapshot{RunID: snapshot.RunID, StreamID: snapshot.StreamID, Attempt: snapshot.Attempt, Sequence: snapshot.Sequence}
	first := header
	first.CandidateContent, first.Plan, first.Blocks = snapshot.CandidateContent, runStreamPlan(snapshot.Plan), []RunStreamBlock{}
	parts := []RunStreamSnapshot{first}
	size := len(snapshot.CandidateContent)
	for _, block := range snapshot.Blocks {
		view := runStreamBlock(block)
		last := &parts[len(parts)-1]
		if len(last.Blocks) > 0 && size+len(view.Text) > budget {
			next := header
			next.Blocks = []RunStreamBlock{}
			parts = append(parts, next)
			last, size = &parts[len(parts)-1], 0
		}
		last.Blocks = append(last.Blocks, view)
		size += len(view.Text)
	}
	for i := range parts {
		parts[i].Part, parts[i].PartCount = i, len(parts)
	}
	return parts
}

// RunStreamDeltaFrame 把运行流增量转换为事件契约中的增量。
func RunStreamDeltaFrame(delta runstream.Delta) RunStreamDelta {
	operations := make([]RunStreamOperation, 0, len(delta.Operations))
	for _, operation := range delta.Operations {
		item := RunStreamOperation{
			Kind:     RunStreamOperationKind(operation.Kind),
			BlockID:  operation.BlockID,
			BlockIDs: operation.BlockIDs,
			Text:     operation.Text,
			Plan:     runStreamPlan(operation.Plan),
		}
		if operation.Block != nil {
			block := runStreamBlock(*operation.Block)
			item.Block = &block
		}
		operations = append(operations, item)
	}
	return RunStreamDelta{RunID: delta.RunID, StreamID: delta.StreamID, Attempt: delta.Attempt,
		BaseSequence: delta.BaseSequence, Sequence: delta.Sequence, Operations: operations}
}

// runStreamBlock 把运行流内容块转换为事件契约中的展示块。
func runStreamBlock(block runstream.Block) RunStreamBlock {
	view := RunStreamBlock{ID: block.ID, Position: block.Position, Kind: block.Kind, Text: block.Text}
	if call := block.ToolCall; call != nil {
		view.ToolCall = &RunStreamToolCall{Name: call.Name, Status: call.Status, StartedAt: call.StartedAt, CompletedAt: call.CompletedAt, Description: call.Description, Activity: call.Activity}
	}
	return view
}

// runStreamPlan 把运行流任务清单转换为事件契约中的任务清单。
func runStreamPlan(plan []runstream.PlanTask) []RunStreamPlanTask {
	if plan == nil {
		return nil
	}
	tasks := make([]RunStreamPlanTask, len(plan))
	for i, task := range plan {
		tasks[i] = RunStreamPlanTask{ID: task.ID, Subject: task.Subject, ActiveForm: task.ActiveForm, Status: task.Status}
	}
	return tasks
}
