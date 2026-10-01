package runstream

import (
	"errors"
	"reflect"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
)

// TestStreamSnapshotApply 验证增量按起止序号应用，重复被忽略，缺口、换流和无法应用的操作不修改快照。
func TestStreamSnapshotApply(t *testing.T) {
	snapshot := Snapshot{RunID: "run", StreamID: "stream"}
	first := Delta{RunID: "run", StreamID: "stream", BaseSequence: 0, Sequence: 1, Operations: []Operation{
		{Kind: OperationUpsertBlock, Block: &Block{ID: "thinking", Position: 1, Kind: domain.AgentRunBlockThinking, Text: "先"}},
		{Kind: OperationAppendBlockText, BlockID: "thinking", Text: "想"},
		{Kind: OperationAppendCandidate, Text: "回答"},
	}}
	if applied, err := snapshot.Apply(first); !applied || err != nil {
		t.Fatalf("apply first delta: applied = %t, error = %v", applied, err)
	}
	if applied, err := snapshot.Apply(first); applied || err != nil {
		t.Fatalf("apply duplicate delta: applied = %t, error = %v", applied, err)
	}
	if _, err := snapshot.Apply(Delta{RunID: "run", StreamID: "stream", BaseSequence: 2, Sequence: 3}); !errors.Is(err, ErrGap) {
		t.Fatalf("apply gap error = %v", err)
	}
	// 合并增量的起点早于快照序号时无法部分应用。
	if _, err := snapshot.Apply(Delta{RunID: "run", StreamID: "stream", BaseSequence: 0, Sequence: 2}); !errors.Is(err, ErrGap) {
		t.Fatalf("apply overlapping delta error = %v", err)
	}
	if _, err := snapshot.Apply(Delta{RunID: "run", StreamID: "retry", BaseSequence: 1, Sequence: 2}); !errors.Is(err, ErrMismatch) {
		t.Fatalf("apply other stream error = %v", err)
	}
	invalid := Delta{RunID: "run", StreamID: "stream", BaseSequence: 1, Sequence: 2, Operations: []Operation{
		{Kind: OperationClearCandidate},
		{Kind: OperationAppendBlockText, BlockID: "missing", Text: "尾部"},
	}}
	if _, err := snapshot.Apply(invalid); err == nil {
		t.Fatal("apply unknown block succeeded")
	}
	if snapshot.Sequence != 1 || snapshot.CandidateContent != "回答" || len(snapshot.Blocks) != 1 || snapshot.Blocks[0].Text != "先想" {
		t.Fatalf("snapshot after rejected deltas = %#v", snapshot)
	}
}

// testStreamOperations 返回覆盖文本追加、候选正文和工具状态更新的操作序列。
func testStreamOperations() []Operation {
	return []Operation{
		{Kind: OperationUpsertBlock, Block: &Block{ID: "thinking", Position: 1, Kind: domain.AgentRunBlockThinking, Text: "先"}},
		{Kind: OperationAppendBlockText, BlockID: "thinking", Text: "想"},
		{Kind: OperationAppendBlockText, BlockID: "thinking", Text: "想"},
		{Kind: OperationAppendCandidate, Text: "我"},
		{Kind: OperationAppendCandidate, Text: "来"},
		{Kind: OperationClearCandidate},
		{Kind: OperationUpsertBlock, Block: &Block{ID: "tool", Position: 2, Kind: domain.AgentRunBlockToolCall, ToolCall: &ToolCall{CallID: "call", Name: "echo", Status: domain.AgentToolCallQueued}}},
		{Kind: OperationUpsertBlock, Block: &Block{ID: "tool", Position: 2, Kind: domain.AgentRunBlockToolCall, ToolCall: &ToolCall{CallID: "call", Name: "echo", Status: domain.AgentToolCallRunning}}},
	}
}

// TestMergeStreamOperations 验证合并后的操作与逐条应用得到相同快照。
func TestMergeStreamOperations(t *testing.T) {
	operations := testStreamOperations()
	stepwise := Snapshot{RunID: "run", StreamID: "stream"}
	for i, operation := range operations {
		if _, err := stepwise.Apply(Delta{RunID: "run", StreamID: "stream", BaseSequence: int64(i), Sequence: int64(i + 1), Operations: []Operation{operation}}); err != nil {
			t.Fatal(err)
		}
	}
	merged := MergeOperations(operations)
	combined := Snapshot{RunID: "run", StreamID: "stream"}
	if _, err := combined.Apply(Delta{RunID: "run", StreamID: "stream", Sequence: 1, Operations: merged}); err != nil {
		t.Fatal(err)
	}
	if len(merged) != 4 || !reflect.DeepEqual(stepwise.Blocks, combined.Blocks) || stepwise.CandidateContent != combined.CandidateContent {
		t.Fatalf("merged = %#v, stepwise = %#v, combined = %#v", merged, stepwise, combined)
	}
	if operations[0].Block.Text != "先" {
		t.Fatalf("merge modified source block: %#v", operations[0].Block)
	}
}

// TestMergeStreamDeltas 验证首尾相接的增量合并后与逐条应用结果一致，不相接或不同流的增量不能合并。
func TestMergeStreamDeltas(t *testing.T) {
	operations := testStreamOperations()
	stepwise := Snapshot{RunID: "run", StreamID: "stream"}
	var merged Delta
	for i, operation := range operations {
		delta := Delta{RunID: "run", StreamID: "stream", BaseSequence: int64(i), Sequence: int64(i + 1), Operations: []Operation{operation}}
		if _, err := stepwise.Apply(delta); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			merged = delta
			continue
		}
		var ok bool
		if merged, ok = MergeDeltas(merged, delta); !ok {
			t.Fatalf("merge delta %d failed", i)
		}
	}
	combined := Snapshot{RunID: "run", StreamID: "stream"}
	if _, err := combined.Apply(merged); err != nil {
		t.Fatal(err)
	}
	if merged.BaseSequence != 0 || merged.Sequence != int64(len(operations)) || len(merged.Operations) != 4 ||
		!reflect.DeepEqual(stepwise.Blocks, combined.Blocks) || stepwise.CandidateContent != combined.CandidateContent || combined.Sequence != stepwise.Sequence {
		t.Fatalf("merged = %#v, stepwise = %#v, combined = %#v", merged, stepwise, combined)
	}
	if _, ok := MergeDeltas(Delta{RunID: "run", StreamID: "stream", Sequence: 1}, Delta{RunID: "run", StreamID: "stream", BaseSequence: 2, Sequence: 3}); ok {
		t.Fatal("merged non-adjacent deltas")
	}
	if _, ok := MergeDeltas(Delta{RunID: "run", StreamID: "stream", Sequence: 1}, Delta{RunID: "run", StreamID: "retry", BaseSequence: 1, Sequence: 2}); ok {
		t.Fatal("merged deltas from different streams")
	}
}

// TestStreamPlanOperations 验证任务清单写入整体替换快照中的清单，相邻的写入合并为最后一次。
func TestStreamPlanOperations(t *testing.T) {
	first := []PlanTask{{ID: "1", Subject: "整理报价", Status: domain.AgentPlanTaskInProgress}}
	second := []PlanTask{{ID: "1", Subject: "整理报价", Status: domain.AgentPlanTaskCompleted}, {ID: "2", Subject: "生成表格", Status: domain.AgentPlanTaskPending}}
	merged := MergeOperations([]Operation{{Kind: OperationSetPlan, Plan: first}, {Kind: OperationSetPlan, Plan: second}})
	if len(merged) != 1 || !reflect.DeepEqual(merged[0].Plan, second) {
		t.Fatalf("merged = %+v", merged)
	}
	snapshot := Snapshot{RunID: "run", StreamID: "stream", Plan: first}
	if _, err := snapshot.Apply(Delta{RunID: "run", StreamID: "stream", BaseSequence: 0, Sequence: 1, Operations: merged}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Plan, second) {
		t.Fatalf("plan = %+v", snapshot.Plan)
	}
}
