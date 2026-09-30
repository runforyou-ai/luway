// Package runstream 定义 Agent 运行流的快照、增量、任务清单和订阅分发。
package runstream

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

var (
	// ErrGap 表示增量序号不连续，接收方需要重新读取快照。
	ErrGap = errors.New("agent run stream sequence gap")
	// ErrMismatch 表示增量不属于当前快照所在的流，接收方需要重新读取快照。
	ErrMismatch = errors.New("agent run stream mismatch")
)

// OperationKind 定义运行流增量中的操作类型。
type OperationKind string

const (
	OperationUpsertBlock     OperationKind = "upsert_block"
	OperationAppendBlockText OperationKind = "append_block_text"
	OperationRemoveBlocks    OperationKind = "remove_blocks"
	OperationAppendCandidate OperationKind = "append_candidate"
	OperationClearCandidate  OperationKind = "clear_candidate"
	OperationSetPlan         OperationKind = "set_plan"
)

// Operation 定义一条可按顺序应用到运行流快照的变更。
type Operation struct {
	Kind     OperationKind
	Block    *Block     // upsert_block 写入的完整块。
	BlockID  string     // append_block_text 追加文本的块。
	BlockIDs []string   // remove_blocks 移除的块。
	Text     string     // append_block_text 与 append_candidate 追加的文本。
	Plan     []PlanTask // set_plan 写入的完整任务清单。
}

// Block 定义运行流中展示的内容块，工具调用只含名称和状态。
type Block struct {
	ID          string
	Position    int64
	ModelCallID string
	Kind        domain.AgentRunBlockKind
	Text        string
	ToolCall    *ToolCall
}

// ToolCall 定义运行流中的工具调用名称、状态和起止时间。
type ToolCall struct {
	CallID      string
	Name        string
	Status      domain.AgentToolCallStatus
	StartedAt   *time.Time
	CompletedAt *time.Time
	Description string // 委派调用的子任务说明，参数完整后取值。
	Activity    string // 子 Agent 正在调用的工具名称，只在执行中的委派调用上取值。
}

// Delta 定义运行流快照从起始序号到终止序号的增量，序号在同一流内从 1 连续递增。
type Delta struct {
	RunID        string
	StreamID     string
	Attempt      int
	BaseSequence int64 // 应用前快照所在的序号。
	Sequence     int64 // 应用后快照所在的序号。
	Operations   []Operation
}

// Snapshot 定义运行流在某个序号上的完整展示状态。
type Snapshot struct {
	RunID            string
	StreamID         string
	Attempt          int
	Sequence         int64
	Blocks           []Block
	CandidateContent string
	Plan             []PlanTask
}

// Apply 应用起始序号与快照序号一致的增量；终止序号不超过快照序号时视为重复返回 false，起始序号不一致、流不一致或操作无法应用时返回错误。
func (s *Snapshot) Apply(delta Delta) (bool, error) {
	if delta.RunID != s.RunID || delta.StreamID != s.StreamID {
		return false, ErrMismatch
	}
	if delta.Sequence <= s.Sequence {
		return false, nil
	}
	if delta.BaseSequence != s.Sequence {
		return false, ErrGap
	}
	// 在副本上应用全部操作，任一操作失败时快照保持原状。
	blocks, candidate, plan := slices.Clone(s.Blocks), s.CandidateContent, s.Plan
	for _, operation := range delta.Operations {
		switch operation.Kind {
		case OperationUpsertBlock:
			index := slices.IndexFunc(blocks, func(block Block) bool { return block.ID == operation.Block.ID })
			if index >= 0 {
				blocks[index] = operation.Block.clone()
			} else {
				blocks = append(blocks, operation.Block.clone())
			}
		case OperationAppendBlockText:
			index := slices.IndexFunc(blocks, func(block Block) bool { return block.ID == operation.BlockID })
			if index < 0 {
				return false, fmt.Errorf("append text to unknown stream block %q", operation.BlockID)
			}
			blocks[index].Text += operation.Text
		case OperationRemoveBlocks:
			for _, id := range operation.BlockIDs {
				index := slices.IndexFunc(blocks, func(block Block) bool { return block.ID == id })
				if index < 0 {
					return false, fmt.Errorf("remove unknown stream block %q", id)
				}
				blocks = slices.Delete(blocks, index, index+1)
			}
		case OperationAppendCandidate:
			candidate += operation.Text
		case OperationClearCandidate:
			candidate = ""
		case OperationSetPlan:
			plan = slices.Clone(operation.Plan)
		default:
			return false, fmt.Errorf("unsupported stream operation %q", operation.Kind)
		}
	}
	s.Blocks, s.CandidateContent, s.Plan, s.Sequence = blocks, candidate, plan, delta.Sequence
	return true, nil
}

// MergeDeltas 把同一流内首尾相接的两条增量合并为一条，不相接时返回 false。
func MergeDeltas(earlier, later Delta) (Delta, bool) {
	if earlier.RunID != later.RunID || earlier.StreamID != later.StreamID || earlier.Sequence != later.BaseSequence {
		return Delta{}, false
	}
	merged := later
	merged.BaseSequence = earlier.BaseSequence
	merged.Operations = MergeOperations(append(slices.Clone(earlier.Operations), later.Operations...))
	return merged, true
}

// TextBytes 返回增量中全部操作携带的文本字节数。
func (d Delta) TextBytes() int {
	total := 0
	for _, operation := range d.Operations {
		total += len(operation.Text)
		if operation.Block != nil {
			total += len(operation.Block.Text)
		}
		for _, task := range operation.Plan {
			total += len(task.Subject) + len(task.ActiveForm)
		}
	}
	return total
}

// Clone 复制快照供独立读取。
func (s Snapshot) Clone() Snapshot {
	s.Plan = slices.Clone(s.Plan)
	s.Blocks = slices.Clone(s.Blocks)
	for i, block := range s.Blocks {
		s.Blocks[i] = block.clone()
	}
	return s
}

// clone 复制内容块及其工具调用和起止时间。
func (b Block) clone() Block {
	if b.ToolCall != nil {
		call := *b.ToolCall
		if call.StartedAt != nil {
			startedAt := *call.StartedAt
			call.StartedAt = &startedAt
		}
		if call.CompletedAt != nil {
			completedAt := *call.CompletedAt
			call.CompletedAt = &completedAt
		}
		b.ToolCall = &call
	}
	return b
}

// MergeOperations 合并相邻的同块文本追加、候选正文追加、同块写入和任务清单写入。
func MergeOperations(operations []Operation) []Operation {
	merged := make([]Operation, 0, len(operations))
	for _, operation := range operations {
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			switch {
			case operation.Kind == OperationAppendBlockText && last.Kind == OperationAppendBlockText && last.BlockID == operation.BlockID,
				operation.Kind == OperationAppendCandidate && last.Kind == OperationAppendCandidate:
				last.Text += operation.Text
				continue
			case operation.Kind == OperationAppendBlockText && last.Kind == OperationUpsertBlock && last.Block.ID == operation.BlockID:
				block := *last.Block
				block.Text += operation.Text
				last.Block = &block
				continue
			case operation.Kind == OperationUpsertBlock && last.Kind == OperationUpsertBlock && last.Block.ID == operation.Block.ID:
				last.Block = operation.Block
				continue
			case operation.Kind == OperationSetPlan && last.Kind == OperationSetPlan:
				last.Plan = operation.Plan
				continue
			}
		}
		merged = append(merged, operation)
	}
	return merged
}
