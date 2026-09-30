package agentruntime

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	acp "github.com/coder/acp-go-sdk"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

// errLocalAgentClientMethod 表示本机 Agent 调用了客户端未声明的能力。
var errLocalAgentClientMethod = errors.New("client capability is not provided")

// localAgentToolNames 把 ACP 工具种类映射为界面已有文案的工具名称，未列出的种类以本机 Agent 给出的标题展示。
var localAgentToolNames = map[acp.ToolKind]string{
	acp.ToolKindRead:    "read_file",
	acp.ToolKindEdit:    "edit_file",
	acp.ToolKindMove:    "edit_file",
	acp.ToolKindDelete:  "delete_file",
	acp.ToolKindSearch:  "grep",
	acp.ToolKindExecute: "execute",
	acp.ToolKindFetch:   "web_fetch",
}

// localAgentRecorder 作为 ACP 客户端接收本机 Agent 的过程更新，维护完整过程、候选回复与任务清单并发布运行流；权限请求一律允许。
type localAgentRecorder struct {
	mu        sync.Mutex
	process   []Block
	positions map[string]int // 工具调用编号到过程下标。
	candidate string         // 本轮最后一个工具调用之后的回复正文。
	lastText  int            // 本轮正在追加的思考或说明块下标，-1 表示没有。
	callID    string         // 本轮的模型调用编号。
	plan      []runstream.PlanTask
	publisher *streamPublisher
}

// newLocalAgentRecorder 创建本机 Agent 过程记录器。
func newLocalAgentRecorder(request LocalAgentRequest) *localAgentRecorder {
	return &localAgentRecorder{
		positions: map[string]int{}, lastText: -1,
		publisher: &streamPublisher{header: runstream.Delta{RunID: request.RunID, StreamID: request.StreamID, Attempt: request.Attempt}, sink: request.OnStream},
	}
}

// beginTurn 开始新一轮：上一轮的候选回复转为说明块，之后的输出归入新的模型调用。
func (r *localAgentRecorder) beginTurn() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settleCandidateLocked()
	r.callID, r.lastText = uuid.NewV7().String(), -1
}

// finalContent 返回最后一轮的回复正文。
func (r *localAgentRecorder) finalContent() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(r.candidate)
}

// result 返回运行结果，内容块与任务清单为副本。
func (r *localAgentRecorder) result(content string, endSeq int64, usage Usage) RunResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RunResult{Content: content, EndSeq: endSeq, Usage: usage, Blocks: slices.Clone(r.process), Plan: slices.Clone(r.plan)}
}

// SessionUpdate 按更新类型记录回复、思考、工具调用与任务清单。
func (r *localAgentRecorder) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	update := notification.Update
	switch {
	case update.AgentMessageChunk != nil:
		if text := update.AgentMessageChunk.Content.Text; text != nil && text.Text != "" {
			r.lastText = -1
			r.candidate += text.Text
			r.publisher.add(runstream.Operation{Kind: runstream.OperationAppendCandidate, Text: text.Text})
		}
	case update.AgentThoughtChunk != nil:
		if text := update.AgentThoughtChunk.Content.Text; text != nil && text.Text != "" {
			r.appendThoughtLocked(text.Text)
		}
	case update.ToolCall != nil:
		call := update.ToolCall
		r.recordToolCallLocked(string(call.ToolCallId), call.Kind, call.Title, call.RawInput, call.Content, call.RawOutput, call.Status)
	case update.ToolCallUpdate != nil:
		call := update.ToolCallUpdate
		var kind acp.ToolKind
		if call.Kind != nil {
			kind = *call.Kind
		}
		var status acp.ToolCallStatus
		if call.Status != nil {
			status = *call.Status
		}
		var title string
		if call.Title != nil {
			title = *call.Title
		}
		r.recordToolCallLocked(string(call.ToolCallId), kind, title, call.RawInput, call.Content, call.RawOutput, status)
	case update.Plan != nil:
		r.plan = make([]runstream.PlanTask, 0, len(update.Plan.Entries))
		for index, entry := range update.Plan.Entries {
			r.plan = append(r.plan, runstream.PlanTask{ID: strconv.Itoa(index + 1), Subject: entry.Content, Status: domain.AgentPlanTaskStatus(entry.Status)})
		}
		r.publisher.add(runstream.Operation{Kind: runstream.OperationSetPlan, Plan: slices.Clone(r.plan)})
	}
	return nil
}

// appendThoughtLocked 把思考追加到本轮正在输出的思考块，没有时先把候选回复转为说明块再新建思考块。
func (r *localAgentRecorder) appendThoughtLocked(text string) {
	if r.lastText >= 0 && r.process[r.lastText].Kind == domain.AgentRunBlockThinking {
		r.process[r.lastText].Payload.Text += text
		r.publisher.add(runstream.Operation{Kind: runstream.OperationAppendBlockText, BlockID: r.process[r.lastText].ID, Text: text})
		return
	}
	r.settleCandidateLocked()
	r.lastText = r.addBlockLocked(domain.AgentRunBlockThinking, BlockPayload{Text: text})
}

// recordToolCallLocked 新建或更新一次工具调用；首次出现时先把候选回复转为调用前的说明块，只更新本机 Agent 给出的字段。
func (r *localAgentRecorder) recordToolCallLocked(id string, kind acp.ToolKind, title string, rawInput any, content []acp.ToolCallContent, rawOutput any, status acp.ToolCallStatus) {
	now := time.Now()
	position, exists := r.positions[id]
	if !exists {
		r.settleCandidateLocked()
		r.lastText = -1
		position = r.addBlockLocked(domain.AgentRunBlockToolCall, BlockPayload{ToolCall: &ToolCall{CallID: id, Status: domain.AgentToolCallRunning, StartedAt: &now}})
		r.positions[id] = position
	}
	call := r.process[position].Payload.ToolCall
	if name := cmp.Or(localAgentToolNames[kind], title); name != "" && (call.Name == "" || localAgentToolNames[kind] != "") {
		call.Name = name
	}
	if rawInput != nil {
		if encoded, err := json.Marshal(rawInput); err == nil {
			call.Arguments = string(encoded)
		}
	} else if call.Arguments == "" && title != "" {
		encoded, _ := json.Marshal(map[string]string{"title": title})
		call.Arguments = string(encoded)
	}
	if output := localAgentToolOutput(content, rawOutput); output != "" {
		call.Result = &output
	}
	switch status {
	case acp.ToolCallStatusCompleted:
		call.Status, call.CompletedAt = domain.AgentToolCallSucceeded, &now
	case acp.ToolCallStatusFailed:
		call.Status, call.CompletedAt = domain.AgentToolCallFailed, &now
		if call.Result != nil {
			call.Error, call.Result = call.Result, nil
		}
	}
	r.publisher.add(runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: r.process[position].streamView()})
}

// settleCandidateLocked 把候选回复转为说明块，后续输出开始新的一段回复。
func (r *localAgentRecorder) settleCandidateLocked() {
	if r.candidate == "" {
		return
	}
	text := r.candidate
	r.candidate = ""
	r.publisher.add(runstream.Operation{Kind: runstream.OperationClearCandidate})
	r.addBlockLocked(domain.AgentRunBlockContent, BlockPayload{Text: text})
}

// addBlockLocked 追加一个内容块并发布，返回其下标。
func (r *localAgentRecorder) addBlockLocked(kind domain.AgentRunBlockKind, payload BlockPayload) int {
	block := Block{ID: uuid.NewV7().String(), Position: int64(len(r.process)), ModelCallID: r.callID, Kind: kind, Payload: payload}
	r.process = append(r.process, block)
	r.publisher.add(runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: block.streamView()})
	return len(r.process) - 1
}

// localAgentToolOutput 提取工具调用的文本结果与文件改动路径，没有时使用原始输出。
func localAgentToolOutput(content []acp.ToolCallContent, rawOutput any) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		switch {
		case item.Content != nil && item.Content.Content.Text != nil:
			parts = append(parts, item.Content.Content.Text.Text)
		case item.Diff != nil:
			parts = append(parts, item.Diff.Path)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if rawOutput == nil {
		return ""
	}
	encoded, err := json.Marshal(rawOutput)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// RequestPermission 选择允许类选项，优先始终允许。
func (r *localAgentRecorder) RequestPermission(_ context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	selected := ""
	for _, option := range request.Options {
		switch option.Kind {
		case acp.PermissionOptionKindAllowAlways:
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		case acp.PermissionOptionKindAllowOnce:
			selected = cmp.Or(selected, string(option.OptionId))
		}
	}
	if selected == "" {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(selected))}, nil
}

// ReadTextFile 拒绝读取，客户端不声明文件能力。
func (r *localAgentRecorder) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errLocalAgentClientMethod
}

// WriteTextFile 拒绝写入，客户端不声明文件能力。
func (r *localAgentRecorder) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errLocalAgentClientMethod
}

// CreateTerminal 拒绝创建终端，客户端不声明终端能力。
func (r *localAgentRecorder) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errLocalAgentClientMethod
}

// KillTerminal 拒绝终端操作，客户端不声明终端能力。
func (r *localAgentRecorder) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errLocalAgentClientMethod
}

// TerminalOutput 拒绝终端操作，客户端不声明终端能力。
func (r *localAgentRecorder) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errLocalAgentClientMethod
}

// ReleaseTerminal 拒绝终端操作，客户端不声明终端能力。
func (r *localAgentRecorder) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errLocalAgentClientMethod
}

// WaitForTerminalExit 拒绝终端操作，客户端不声明终端能力。
func (r *localAgentRecorder) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errLocalAgentClientMethod
}
