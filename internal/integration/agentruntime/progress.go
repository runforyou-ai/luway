package agentruntime

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"
	"uuid"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

// Block 定义按模型返回顺序排列的完整中间内容。
type Block struct {
	ID          string                   `json:"id"`
	Position    int64                    `json:"position"`
	ModelCallID string                   `json:"modelCallId"`
	Kind        domain.AgentRunBlockKind `json:"kind"`
	Payload     BlockPayload             `json:"payload"`
}

// BlockPayload 完整保存文本、工具调用参数和结果。
type BlockPayload struct {
	Text     string    `json:"text,omitempty"`
	ToolCall *ToolCall `json:"toolCall,omitempty"`
}

// ToolCall 保存一次模型指定的工具调用，包括可供模型修正的失败。
type ToolCall struct {
	ID          string                     `json:"id"`                 // 调用编号。
	ParentID    string                     `json:"parentId,omitempty"` // 子 Agent 的调用所属的委派调用编号。
	ModelCallID string                     `json:"modelCallId"`        // 发起调用的模型调用编号，子 Agent 的调用取所属委派调用的模型调用编号。
	CallID      string                     `json:"callId"`
	Name        string                     `json:"name"`
	Source      domain.AgentToolSource     `json:"source"`
	Replayable  bool                       `json:"replayable"`
	SideEffects bool                       `json:"sideEffects"`
	Arguments   string                     `json:"arguments"`
	Result      *string                    `json:"result"`
	Error       *string                    `json:"error"`
	Status      domain.AgentToolCallStatus `json:"status"`
	StartedAt   *time.Time                 `json:"startedAt"`
	CompletedAt *time.Time                 `json:"completedAt"`
	MCPServer   string                     `json:"mcpServer,omitempty"` // MCP 工具所属的服务名称，内置工具为空。
	Evidence    bool                       `json:"evidence,omitempty"`  // 原始结果通过依据判定。
	Activity    string                     `json:"-"`                   // 子 Agent 正在调用的工具名称，只进入运行流。
}

// streamView 返回内容块在运行流中的展示形态，工具调用不含参数和结果。
func (b Block) streamView() *runstream.Block {
	view := &runstream.Block{ID: b.ID, Position: b.Position, ModelCallID: b.ModelCallID, Kind: b.Kind, Text: b.Payload.Text}
	if call := b.Payload.ToolCall; call != nil {
		view.ToolCall = &runstream.ToolCall{CallID: call.CallID, Name: call.Name, Status: call.Status, StartedAt: call.StartedAt, CompletedAt: call.CompletedAt, Activity: call.Activity}
		// 委派调用的参数完整后取出子任务说明。
		if call.Name == subagentToolName && call.MCPServer == "" {
			var arguments struct {
				Description string `json:"description"`
			}
			if json.Unmarshal([]byte(call.Arguments), &arguments) == nil {
				view.ToolCall.Description = arguments.Description
			}
		}
	}
	return view
}

// processRecorder 在内存中维护一次执行尝试的完整过程，把展示变化交给运行流发布，并在安全点交给日志持久化。
type processRecorder struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	mu             sync.Mutex
	process        []Block
	children       []ToolCall // 子 Agent 发起的工具调用，按开始顺序排列。
	childPositions map[string]int
	candidate      string
	toolPositions  map[string]int
	mcpTools       map[string]mcpToolRef // 本次运行挂载的 MCP 工具按模型可见名称索引，运行开始前写入。
	traits         map[string]toolTraits // 本次运行注册的工具按模型可见名称索引，运行开始前写入。
	plan           []runstream.PlanTask  // 本次运行的任务清单，按任务编号排列。
	call           *modelCallStream
	publisher      *streamPublisher
	journal        Journal
	usage          Usage                                                 // 对话模型输出的累计用量，模型输出定稿时累计。
	onStep         func(context.Context, []*schema.AgenticMessage) error // 模型输出定稿后保存安全点，运行开始前写入。
	onToolFinished func(context.Context) error                           // 主 Agent 的工具调用结束后保存执行期状态，运行开始前写入。
}

// toolTraits 是工具的来源、中断后能否重新执行与是否产生外部副作用。
type toolTraits struct {
	source      domain.AgentToolSource
	replayable  bool
	sideEffects bool
}

// modelCallStream 记录一次尚未定稿的模型调用中流式分片序号与内容块位置的对应关系。
type modelCallStream struct {
	id             string
	start          int
	indices        []int
	positions      map[int]int
	nextOrdinal    int
	candidateParts []candidatePart
	hasTools       bool
}

// candidatePart 定义候选正文中来自同一分片序号的一段文本。
type candidatePart struct {
	index int
	text  string
}

// newProcessRecorder 为每次执行创建独立的内容缓冲和运行流发布器。
func newProcessRecorder(request RunRequest) *processRecorder {
	streamID := request.StreamID
	if streamID == "" {
		streamID = uuid.NewV7().String()
	}
	return &processRecorder{
		toolPositions: make(map[string]int), childPositions: make(map[string]int), traits: make(map[string]toolTraits),
		publisher: &streamPublisher{header: runstream.Delta{RunID: request.RunID, StreamID: streamID, Attempt: request.Attempt}, sink: request.OnStream},
		journal:   request.Journal,
	}
}

// streamingModel 在模型流式分片被读取时交给过程记录器。
type streamingModel struct {
	model.BaseModel[*schema.AgenticMessage]
	recorder *processRecorder
}

// WrapModel 为每次模型调用接入流式分片记录。
func (r *processRecorder) WrapModel(_ context.Context, m model.BaseModel[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (model.BaseModel[*schema.AgenticMessage], error) {
	return &streamingModel{BaseModel: m, recorder: r}, nil
}

// Stream 为模型调用分配编号并开始记录，分片经过时同步累积并登记增量。
func (m *streamingModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	callID := uuid.NewV7().String()
	reader, err := m.BaseModel.Stream(context.WithValue(ctx, modelCallIDContextKey{}, callID), input, opts...)
	if err != nil {
		return nil, err
	}
	m.recorder.mu.Lock()
	m.recorder.beginCallLocked(callID)
	m.recorder.mu.Unlock()
	return schema.StreamReaderWithConvert(reader, func(chunk *schema.AgenticMessage) (*schema.AgenticMessage, error) {
		if chunk != nil {
			m.recorder.receive(chunk)
		}
		return chunk, nil
	}), nil
}

// beginCallLocked 以给定编号开始一次模型调用并清空上一调用的候选正文，编号为空时新分配；上一调用未定稿即被重试时移除其内容块。调用方持有缓冲锁。
func (r *processRecorder) beginCallLocked(id string) *modelCallStream {
	if r.call != nil && len(r.process) > r.call.start {
		removed := make([]string, 0, len(r.process)-r.call.start)
		for _, block := range r.process[r.call.start:] {
			removed = append(removed, block.ID)
		}
		r.process = r.process[:r.call.start]
		r.publisher.add(runstream.Operation{Kind: runstream.OperationRemoveBlocks, BlockIDs: removed})
	}
	r.call = &modelCallStream{id: cmp.Or(id, uuid.NewV7().String()), start: len(r.process), positions: make(map[int]int)}
	if r.candidate != "" {
		r.candidate = ""
		r.publisher.add(runstream.Operation{Kind: runstream.OperationClearCandidate})
	}
	return r.call
}

// receive 按分片序号累积思考、正文和工具调用，没有工具调用前的正文作为候选回复。
func (r *processRecorder) receive(chunk *schema.AgenticMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := r.call
	if call == nil {
		return
	}
	for _, block := range chunk.ContentBlocks {
		if block == nil {
			continue
		}
		// 流式分片使用模型给出的序号，整块输出按出现顺序编号。
		index := call.nextOrdinal
		if block.StreamingMeta != nil {
			index = block.StreamingMeta.Index
		} else {
			call.nextOrdinal++
		}
		if !slices.Contains(call.indices, index) {
			call.indices = append(call.indices, index)
		}
		position, exists := call.positions[index]
		switch block.Type {
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning.Text == "" {
				continue
			}
			if exists {
				r.appendTextLocked(position, block.Reasoning.Text)
			} else {
				r.addBlockLocked(index, domain.AgentRunBlockThinking, BlockPayload{Text: block.Reasoning.Text})
			}
		case schema.ContentBlockTypeAssistantGenText:
			text := block.AssistantGenText.Text
			switch {
			case text == "":
			case !call.hasTools:
				if last := len(call.candidateParts) - 1; last >= 0 && call.candidateParts[last].index == index {
					call.candidateParts[last].text += text
				} else {
					call.candidateParts = append(call.candidateParts, candidatePart{index: index, text: text})
				}
				r.candidate += text
				r.publisher.add(runstream.Operation{Kind: runstream.OperationAppendCandidate, Text: text})
			case exists:
				r.appendTextLocked(position, text)
			default:
				r.addBlockLocked(index, domain.AgentRunBlockContent, BlockPayload{Text: text})
			}
		case schema.ContentBlockTypeFunctionToolCall:
			// 首个工具调用出现后，已输出的候选正文按分片序号转为调用前的说明块。
			if !call.hasTools {
				call.hasTools = true
				if r.candidate != "" {
					r.candidate = ""
					r.publisher.add(runstream.Operation{Kind: runstream.OperationClearCandidate})
					for _, part := range call.candidateParts {
						r.addBlockLocked(part.index, domain.AgentRunBlockContent, BlockPayload{Text: part.text})
					}
				}
			}
			chunkCall := block.FunctionToolCall
			if !exists {
				r.addBlockLocked(index, domain.AgentRunBlockToolCall, BlockPayload{ToolCall: r.queuedToolCall(chunkCall, call.id)})
				continue
			}
			recorded := r.process[position].Payload.ToolCall
			recorded.Arguments += chunkCall.Arguments
			// 调用编号和工具名称在后续分片中才出现时补齐并发布。
			if (recorded.CallID == "" && chunkCall.CallID != "") || (recorded.Name == "" && chunkCall.Name != "") {
				recorded.CallID = cmp.Or(recorded.CallID, chunkCall.CallID)
				if recorded.Name == "" {
					r.nameToolCall(recorded, chunkCall.Name)
				}
				r.publisher.add(runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: r.process[position].streamView()})
			}
		}
	}
}

// mcpToolRef 是 MCP 工具所属的服务名称与服务目录中的原工具名。
type mcpToolRef struct {
	server string
	name   string
}

// queuedToolCall 按模型指定的调用创建排队中的工具调用并分配调用编号，MCP 工具记录原工具名与所属服务。
func (r *processRecorder) queuedToolCall(call *schema.FunctionToolCall, modelCallID string) *ToolCall {
	recorded := &ToolCall{ID: uuid.NewV7().String(), ModelCallID: modelCallID, CallID: call.CallID, Arguments: call.Arguments, Status: domain.AgentToolCallQueued}
	r.nameToolCall(recorded, call.Name)
	return recorded
}

// nameToolCall 按模型可见名称填写调用的工具名、所属 MCP 服务与工具特性；未登记的工具按不可重新执行且有副作用处理。
func (r *processRecorder) nameToolCall(call *ToolCall, name string) {
	call.Name = name
	traits, ok := r.traits[name]
	if !ok {
		traits = toolTraits{source: domain.AgentToolSourceBuiltin, sideEffects: true}
	}
	call.Source, call.Replayable, call.SideEffects = traits.source, traits.replayable, traits.sideEffects
	if ref, ok := r.mcpTools[name]; ok {
		call.Name, call.MCPServer = ref.name, ref.server
	}
}

// addBlockLocked 为当前模型调用追加内容块并登记分片序号，调用方持有缓冲锁。
func (r *processRecorder) addBlockLocked(index int, kind domain.AgentRunBlockKind, payload BlockPayload) {
	position := len(r.process)
	r.process = append(r.process, Block{ID: uuid.NewV7().String(), Position: int64(position + 1), ModelCallID: r.call.id, Kind: kind, Payload: payload})
	r.call.positions[index] = position
	r.publisher.add(runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: r.process[position].streamView()})
}

// appendTextLocked 向已有文本块追加分片文本，调用方持有缓冲锁。
func (r *processRecorder) appendTextLocked(position int, text string) {
	r.process[position].Payload.Text += text
	r.publisher.add(runstream.Operation{Kind: runstream.OperationAppendBlockText, BlockID: r.process[position].ID, Text: text})
}

// AfterModelRewriteState 在工具执行前按完整模型输出定稿本次调用的内容块，沿用流式阶段分配的块与调用编号，随后保存安全点。
func (r *processRecorder) AfterModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	r.finalizeCall(state.Messages[len(state.Messages)-1])
	if r.onStep != nil {
		if err := r.onStep(ctx, state.Messages); err != nil {
			return ctx, state, err
		}
	}
	return ctx, state, nil
}

// finalizeCall 按完整模型输出定稿当前模型调用的内容块并累计用量。
func (r *processRecorder) finalizeCall(message *schema.AgenticMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage.add(message.ResponseMeta)
	call := r.call
	if call == nil {
		call = r.beginCallLocked("")
	}
	r.call = nil
	// 完整输出的第 i 个内容块由第 i 小的分片序号拼接而成，数量一致时才能沿用流式块编号。
	indices := slices.Sorted(slices.Values(call.indices))
	keyed := len(indices) == len(message.ContentBlocks)
	withCalls := hasToolCalls(message)
	var final []Block
	for i, block := range message.ContentBlocks {
		var kind domain.AgentRunBlockKind
		var payload BlockPayload
		switch {
		case block.Type == schema.ContentBlockTypeReasoning && block.Reasoning.Text != "":
			kind, payload = domain.AgentRunBlockThinking, BlockPayload{Text: block.Reasoning.Text}
		case block.Type == schema.ContentBlockTypeAssistantGenText && withCalls && block.AssistantGenText.Text != "":
			kind, payload = domain.AgentRunBlockContent, BlockPayload{Text: block.AssistantGenText.Text}
		case block.Type == schema.ContentBlockTypeFunctionToolCall:
			kind, payload = domain.AgentRunBlockToolCall, BlockPayload{ToolCall: r.queuedToolCall(block.FunctionToolCall, call.id)}
		default:
			continue
		}
		id := uuid.NewV7().String()
		if keyed {
			if position, ok := call.positions[indices[i]]; ok && r.process[position].Kind == kind {
				id = r.process[position].ID
				if streamed := r.process[position].Payload.ToolCall; streamed != nil {
					payload.ToolCall.ID = streamed.ID
				}
			}
		}
		final = append(final, Block{ID: id, ModelCallID: call.id, Kind: kind, Payload: payload})
	}
	// 块编号顺序与流式阶段一致时只发布有变化的块，否则整体替换本次调用的块；定稿操作一次登记，同一增量内原子应用。
	var operations []runstream.Operation
	streamed := slices.Clone(r.process[call.start:])
	sameOrder := slices.EqualFunc(streamed, final, func(a, b Block) bool { return a.ID == b.ID })
	if !sameOrder && len(streamed) > 0 {
		slog.Warn("Agent 模型调用定稿的内容块与流式阶段不一致，整体替换",
			"agent_run_id", r.publisher.header.RunID, "stream_id", r.publisher.header.StreamID,
			"streamed_blocks", len(streamed), "final_blocks", len(final))
		removed := make([]string, len(streamed))
		for i, block := range streamed {
			removed[i] = block.ID
		}
		operations = append(operations, runstream.Operation{Kind: runstream.OperationRemoveBlocks, BlockIDs: removed})
	}
	r.process = r.process[:call.start]
	for i, block := range final {
		block.Position = int64(call.start + i + 1)
		if block.Payload.ToolCall != nil {
			r.toolPositions[block.Payload.ToolCall.CallID] = call.start + i
		}
		r.process = append(r.process, block)
		if view := block.streamView(); !sameOrder || !reflect.DeepEqual(streamed[i].streamView(), view) {
			operations = append(operations, runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: view})
		}
	}
	candidate := ""
	if !withCalls {
		candidate = assistantText(message)
	}
	if candidate != r.candidate {
		r.candidate = candidate
		operations = append(operations, runstream.Operation{Kind: runstream.OperationClearCandidate})
		if candidate != "" {
			operations = append(operations, runstream.Operation{Kind: runstream.OperationAppendCandidate, Text: candidate})
		}
	}
	r.publisher.add(operations...)
}

// updateTool 更新原位置上的工具状态并写入日志，允许并行工具乱序完成。
func (r *processRecorder) updateTool(ctx context.Context, callID string, update func(*ToolCall)) error {
	r.mu.Lock()
	position, ok := r.toolPositions[callID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("agent tool call %q has no model output", callID)
	}
	call := r.process[position].Payload.ToolCall
	update(call)
	saved := *call
	r.publisher.add(runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: r.process[position].streamView()})
	r.mu.Unlock()
	return r.saveToolCall(ctx, saved)
}

// saveToolCall 把工具调用的当前状态写入日志，没有日志时忽略。
func (r *processRecorder) saveToolCall(ctx context.Context, call ToolCall) error {
	if r.journal == nil {
		return nil
	}
	return r.journal.SaveToolCall(ctx, call)
}

// childStarted 登记子 Agent 开始执行的工具调用，归属所在的委派调用，并把它记为委派调用的当前活动。
func (r *processRecorder) childStarted(ctx context.Context, parentCallID string, input *compose.ToolInput, at time.Time) error {
	r.mu.Lock()
	position, ok := r.toolPositions[parentCallID]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	parent := r.process[position].Payload.ToolCall
	call := ToolCall{ID: uuid.NewV7().String(), ParentID: parent.ID, ModelCallID: parent.ModelCallID, CallID: input.CallID,
		Arguments: input.Arguments, Status: domain.AgentToolCallRunning, StartedAt: &at}
	r.nameToolCall(&call, input.Name)
	r.childPositions[parent.ID+"/"+input.CallID] = len(r.children)
	r.children = append(r.children, call)
	r.setActivityLocked(position, call.Name)
	r.mu.Unlock()
	return r.saveToolCall(ctx, call)
}

// childFinished 记录子 Agent 工具调用的结果或错误。
func (r *processRecorder) childFinished(ctx context.Context, parentCallID string, input *compose.ToolInput, at time.Time, result string, err error) error {
	r.mu.Lock()
	position, ok := r.toolPositions[parentCallID]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	index, ok := r.childPositions[r.process[position].Payload.ToolCall.ID+"/"+input.CallID]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	call := &r.children[index]
	settleToolCall(call, at, result, err)
	saved := *call
	r.mu.Unlock()
	return r.saveToolCall(ctx, saved)
}

// childCalls 返回子 Agent 发起的工具调用副本。
func (r *processRecorder) childCalls() []ToolCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.children)
}

// waiting 判断是否有工具调用正在等待外部结果。
func (r *processRecorder) waiting() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.process, func(block Block) bool {
		return block.Payload.ToolCall != nil && block.Payload.ToolCall.Status == domain.AgentToolCallWaiting
	})
}

// setActivityLocked 记录委派调用中子 Agent 正在调用的工具，委派调用已结束时忽略。调用方持有缓冲锁。
func (r *processRecorder) setActivityLocked(position int, name string) {
	call := r.process[position].Payload.ToolCall
	if call.Status != domain.AgentToolCallRunning || call.Activity == name {
		return
	}
	call.Activity = name
	r.publisher.add(runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: r.process[position].streamView()})
}

// setPlan 替换任务清单并发布到运行流。
func (r *processRecorder) setPlan(plan []runstream.PlanTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plan = plan
	r.publisher.add(runstream.Operation{Kind: runstream.OperationSetPlan, Plan: slices.Clone(plan)})
}

// currentPlan 返回任务清单的副本。
func (r *processRecorder) currentPlan() []runstream.PlanTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.plan)
}

// markEvidence 标记工具调用的原始结果构成回答依据。
func (r *processRecorder) markEvidence(callID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if position, ok := r.toolPositions[callID]; ok {
		r.process[position].Payload.ToolCall.Evidence = true
	}
}

// resetCandidate 在安全点补入新消息时丢弃候选正文、未完成模型调用的内容和被跳过的工具。
func (r *processRecorder) resetCandidate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []string
	if r.call != nil {
		for _, block := range r.process[r.call.start:] {
			removed = append(removed, block.ID)
		}
		r.process, r.call = r.process[:r.call.start], nil
	}
	// AfterChatModel 安全点可跳过整批未启动工具，已返回的思考和说明文本仍保留。
	for len(r.process) > 0 {
		last := r.process[len(r.process)-1]
		if last.Payload.ToolCall == nil || last.Payload.ToolCall.Status != domain.AgentToolCallQueued {
			break
		}
		delete(r.toolPositions, last.Payload.ToolCall.CallID)
		removed = append(removed, last.ID)
		r.process = r.process[:len(r.process)-1]
	}
	if len(removed) > 0 {
		r.publisher.add(runstream.Operation{Kind: runstream.OperationRemoveBlocks, BlockIDs: removed})
	}
	if r.candidate != "" {
		r.candidate = ""
		r.publisher.add(runstream.Operation{Kind: runstream.OperationClearCandidate})
	}
}

// blocks 返回成功时应持久化的中间内容。
func (r *processRecorder) blocks() []Block {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneBlocks(r.process)
}

// cloneBlocks 复制内容块及其工具调用，副本不随之后的工具状态变化。
func cloneBlocks(blocks []Block) []Block {
	cloned := slices.Clone(blocks)
	for index := range cloned {
		if call := cloned[index].Payload.ToolCall; call != nil {
			copied := *call
			cloned[index].Payload.ToolCall = &copied
		}
	}
	return cloned
}

// partialBlocks 返回运行中断时应持久化的内容，尚未定稿的回复正文补在末尾，否则断流时已输出的正文无处可读。
func (r *processRecorder) partialBlocks() []Block {
	r.mu.Lock()
	defer r.mu.Unlock()
	blocks := cloneBlocks(r.process)
	if r.candidate == "" {
		return blocks
	}
	// 候选正文属于最后一次模型调用，该调用尚未定稿时取其编号，已定稿时沿用它留下的末块编号。
	modelCallID := ""
	if r.call != nil {
		modelCallID = r.call.id
	} else if len(blocks) > 0 {
		modelCallID = blocks[len(blocks)-1].ModelCallID
	}
	if modelCallID == "" {
		modelCallID = uuid.NewV7().String()
	}
	return append(blocks, Block{ID: uuid.NewV7().String(), Position: int64(len(blocks) + 1),
		ModelCallID: modelCallID, Kind: domain.AgentRunBlockContent, Payload: BlockPayload{Text: r.candidate}})
}

// restore 以已保存的内容块与子 Agent 调用作为本次执行的起点，并整体发布到运行流。
func (r *processRecorder) restore(blocks []Block, children []ToolCall, plan []runstream.PlanTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.process = slices.Clone(blocks)
	for position, block := range r.process {
		if call := block.Payload.ToolCall; call != nil {
			r.toolPositions[call.CallID] = position
		}
	}
	r.children = slices.Clone(children)
	for index, call := range r.children {
		r.childPositions[call.ParentID+"/"+call.CallID] = index
	}
	r.plan = slices.Clone(plan)
	operations := make([]runstream.Operation, 0, len(r.process)+1)
	for _, block := range r.process {
		operations = append(operations, runstream.Operation{Kind: runstream.OperationUpsertBlock, Block: block.streamView()})
	}
	if len(plan) > 0 {
		operations = append(operations, runstream.Operation{Kind: runstream.OperationSetPlan, Plan: slices.Clone(plan)})
	}
	r.publisher.add(operations...)
}

// modelUsage 返回对话模型输出的累计用量。
func (r *processRecorder) modelUsage() Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}
