package agentruntime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

type processChatModel struct {
	generate func(context.Context, []*schema.AgenticMessage) (*schema.AgenticMessage, error)
}

// TestRecorderDropsSkippedTools 验证安全点补入新输入后不保留尚未执行的工具。
func TestRecorderDropsSkippedTools(t *testing.T) {
	recorder := newProcessRecorder(RunRequest{RunID: "run"})
	message := withReasoning(assistantReply("准备计算",
		&schema.FunctionToolCall{CallID: "skipped-1", Name: "echo", Arguments: "{}"},
		&schema.FunctionToolCall{CallID: "skipped-2", Name: "echo", Arguments: "{}"},
	), "先分析旧问题")
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: []*schema.AgenticMessage{message}}
	if _, _, err := recorder.AfterModelRewriteState(context.Background(), state, nil); err != nil {
		t.Fatal(err)
	}
	before := recorder.blocks()
	recorder.resetCandidate()
	state.Messages = append(state.Messages, assistantReply("根据补充信息得出的结果"))
	if _, _, err := recorder.AfterModelRewriteState(context.Background(), state, nil); err != nil {
		t.Fatal(err)
	}
	after := recorder.blocks()
	if len(after) != 2 || after[0].ID != before[0].ID || after[1].ID != before[1].ID || len(recorder.toolPositions) != 0 {
		t.Fatalf("blocks after safe point = %#v", after)
	}
	if recorder.candidate != "根据补充信息得出的结果" {
		t.Fatalf("candidate content = %q", recorder.candidate)
	}
}

// TestRecorderDropsUnfinishedModelCall 验证模型调用被抢占时不保留已经流出的内容。
func TestRecorderDropsUnfinishedModelCall(t *testing.T) {
	recorder := newProcessRecorder(RunRequest{RunID: "run", OnStream: func(runstream.Delta) {}})
	recorder.mu.Lock()
	recorder.beginCallLocked("")
	recorder.mu.Unlock()
	recorder.receive(&schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{
		schema.NewContentBlockChunk(&schema.Reasoning{Text: "先分析"}, &schema.StreamingMeta{Index: 0}),
		schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "部分回答"}, &schema.StreamingMeta{Index: 1}),
	}})
	if len(recorder.blocks()) != 1 || recorder.candidate != "部分回答" {
		t.Fatalf("streamed blocks = %#v, candidate = %q", recorder.blocks(), recorder.candidate)
	}
	thinkingID := recorder.blocks()[0].ID
	recorder.publisher.pending = nil
	recorder.resetCandidate()
	if len(recorder.blocks()) != 0 || recorder.candidate != "" || recorder.call != nil {
		t.Fatalf("blocks after preemption = %#v, candidate = %q", recorder.blocks(), recorder.candidate)
	}
	want := []runstream.Operation{{Kind: runstream.OperationRemoveBlocks, BlockIDs: []string{thinkingID}}, {Kind: runstream.OperationClearCandidate}}
	if !reflect.DeepEqual(recorder.publisher.pending, want) {
		t.Fatalf("operations after preemption = %#v", recorder.publisher.pending)
	}
}

// TestRecorderKeepsStreamedBlockIDs 验证工具调用前的多段正文按分片序号成块，定稿沿用编号且不整体替换。
func TestRecorderKeepsStreamedBlockIDs(t *testing.T) {
	recorder := newProcessRecorder(RunRequest{RunID: "run", OnStream: func(runstream.Delta) {}})
	chunks := []*schema.AgenticMessage{
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlockChunk(&schema.Reasoning{Text: "想"}, &schema.StreamingMeta{Index: 0})}},
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "说明一"}, &schema.StreamingMeta{Index: 1})}},
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "说明二"}, &schema.StreamingMeta{Index: 2})}},
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlockChunk(&schema.FunctionToolCall{CallID: "call", Name: "echo", Arguments: "{}"}, &schema.StreamingMeta{Index: 3})}},
	}
	recorder.mu.Lock()
	recorder.beginCallLocked("")
	recorder.mu.Unlock()
	for _, chunk := range chunks {
		recorder.receive(chunk)
	}
	streamed := recorder.blocks()
	message, err := schema.ConcatAgenticMessages(chunks)
	if err != nil {
		t.Fatal(err)
	}
	recorder.publisher.pending = nil
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: []*schema.AgenticMessage{message}}
	if _, _, err := recorder.AfterModelRewriteState(context.Background(), state, nil); err != nil {
		t.Fatal(err)
	}
	final := recorder.blocks()
	if len(streamed) != 4 || len(final) != 4 || streamed[1].Payload.Text != "说明一" || streamed[2].Payload.Text != "说明二" || len(recorder.publisher.pending) != 0 {
		t.Fatalf("streamed = %#v, final = %#v, operations = %#v", streamed, final, recorder.publisher.pending)
	}
	for i := range final {
		if final[i].ID != streamed[i].ID {
			t.Fatalf("block %d id changed: streamed = %#v, final = %#v", i, streamed[i], final[i])
		}
	}
}

// Generate 返回当前测试步骤指定的模型输出。
func (m *processChatModel) Generate(ctx context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	return m.generate(ctx, input)
}

// Stream 以单个分片返回当前测试步骤的模型输出。
func (m *processChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestRunRecordsToolCorrection 验证工具乱序完成、参数修正和成功过程的完整顺序。
func TestRunRecordsToolCorrection(t *testing.T) {
	runtime, err := newEchoRuntime()
	if err != nil {
		t.Fatal(err)
	}
	modelCalls := 0
	chatModel := &processChatModel{generate: func(_ context.Context, messages []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return withReasoning(assistantReply("先计算两个结果",
				&schema.FunctionToolCall{CallID: "slow", Name: "echo", Arguments: `{"text":"3","delayMilliseconds":300}`},
				&schema.FunctionToolCall{CallID: "invalid", Name: "echo", Arguments: `{"text":`},
			), "需要分两步计算"), nil
		case 2:
			foundError := false
			for _, message := range messages {
				if result := toolResult(message); result != nil && result.CallID == "invalid" && strings.Contains(messageText(message), `"error"`) {
					foundError = true
				}
			}
			if !foundError {
				return nil, errors.New("model did not receive tool error")
			}
			return assistantReply("修正参数再试一次",
				&schema.FunctionToolCall{CallID: "corrected", Name: "echo", Arguments: `{"text":"12"}`},
			), nil
		default:
			return withReasoning(assistantReply("最终结果为 12"), "两个步骤均已完成"), nil
		}
	}}
	feed := &testInputFeed{}
	feed.appendUser("开始计算")
	var deltas []runstream.Delta
	result, err := runtime.Run(context.Background(), RunRequest{RunID: "run", Assignment: Assignment{AgentName: "test"}, Models: fixedModels(chatModel), StreamID: "stream", Attempt: 2, OnStream: func(delta runstream.Delta) {
		deltas = append(deltas, delta)
	}}, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "最终结果为 12" || modelCalls != 3 || len(result.Blocks) != 7 {
		t.Fatalf("result = %#v, model calls = %d", result, modelCalls)
	}
	wantKinds := []domain.AgentRunBlockKind{domain.AgentRunBlockThinking, domain.AgentRunBlockContent, domain.AgentRunBlockToolCall, domain.AgentRunBlockToolCall, domain.AgentRunBlockContent, domain.AgentRunBlockToolCall, domain.AgentRunBlockThinking}
	for i, block := range result.Blocks {
		if block.Position != int64(i+1) || block.Kind != wantKinds[i] || block.ID == "" || block.ModelCallID == "" {
			t.Fatalf("block %d = %#v", i, block)
		}
	}
	failed, corrected := result.Blocks[3].Payload.ToolCall, result.Blocks[5].Payload.ToolCall
	if failed.Status != domain.AgentToolCallFailed || failed.Arguments != `{"text":` || failed.Error == nil || *failed.Error == "" || failed.Result != nil {
		t.Fatalf("failed call = %#v", failed)
	}
	if corrected.Status != domain.AgentToolCallSucceeded || corrected.Result == nil || !strings.Contains(*corrected.Result, "12") || corrected.Error != nil {
		t.Fatalf("corrected call = %#v", corrected)
	}
	snapshot := runstream.Snapshot{RunID: "run", StreamID: "stream", Attempt: 2}
	outOfOrder := false
	for _, delta := range deltas {
		if applied, err := snapshot.Apply(delta); !applied || err != nil || delta.Attempt != 2 {
			t.Fatalf("apply delta %#v: applied = %t, error = %v", delta, applied, err)
		}
		if len(snapshot.Blocks) >= 4 && snapshot.Blocks[2].ToolCall.Status != domain.AgentToolCallSucceeded && snapshot.Blocks[3].ToolCall.Status == domain.AgentToolCallFailed {
			outOfOrder = true
		}
	}
	if !outOfOrder || snapshot.CandidateContent != result.Content || len(snapshot.Blocks) != len(result.Blocks) {
		t.Fatalf("missing out-of-order tool snapshot or final candidate: %#v", snapshot)
	}
	for i, block := range snapshot.Blocks {
		if !reflect.DeepEqual(&block, result.Blocks[i].streamView()) {
			t.Fatalf("stream block %d = %#v, persisted = %#v", i, block, result.Blocks[i])
		}
	}
}

type chunkedChatModel struct {
	calls  int
	stream func(int, *schema.StreamWriter[*schema.AgenticMessage])
}

// Generate 拒绝流式测试未使用的整体调用。
func (m *chunkedChatModel) Generate(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.AgenticMessage, error) {
	return nil, errors.New("unexpected generate call")
}

// Stream 在独立协程中按测试步骤写出分片。
func (m *chunkedChatModel) Stream(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.calls++
	reader, writer := schema.Pipe[*schema.AgenticMessage](8)
	go func(call int) {
		defer writer.Close()
		m.stream(call, writer)
	}(m.calls)
	return reader, nil
}

// TestRunStreamsModelChunks 验证模型调用尚未结束时已发布增量，定稿后块编号不变且工具参数不进入运行流。
func TestRunStreamsModelChunks(t *testing.T) {
	runtime, err := newEchoRuntime()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	snapshot := runstream.Snapshot{RunID: "run", StreamID: "stream"}
	var midStream runstream.Snapshot
	candidateStreamed := make(chan struct{})
	chunk := func(block *schema.ContentBlock) *schema.AgenticMessage {
		return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{block}}
	}
	chatModel := &chunkedChatModel{stream: func(call int, writer *schema.StreamWriter[*schema.AgenticMessage]) {
		if call > 1 {
			writer.Send(chunk(schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "结果"}, &schema.StreamingMeta{Index: 0})), nil)
			writer.Send(chunk(schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "是 3"}, &schema.StreamingMeta{Index: 0})), nil)
			return
		}
		writer.Send(chunk(schema.NewContentBlockChunk(&schema.Reasoning{Text: "先"}, &schema.StreamingMeta{Index: 0})), nil)
		writer.Send(chunk(schema.NewContentBlockChunk(&schema.Reasoning{Text: "想想"}, &schema.StreamingMeta{Index: 0})), nil)
		writer.Send(chunk(schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "我来"}, &schema.StreamingMeta{Index: 1})), nil)
		writer.Send(chunk(schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "算"}, &schema.StreamingMeta{Index: 1})), nil)
		// 候选正文经运行流发布后才输出工具调用，证明增量不等待模型调用结束。
		select {
		case <-candidateStreamed:
		case <-ctx.Done():
			return
		}
		writer.Send(chunk(schema.NewContentBlockChunk(&schema.FunctionToolCall{CallID: "add", Name: "echo", Arguments: `{"text":`}, &schema.StreamingMeta{Index: 2})), nil)
		writer.Send(chunk(schema.NewContentBlockChunk(&schema.FunctionToolCall{Arguments: `"3"}`}, &schema.StreamingMeta{Index: 2})), nil)
	}}
	feed := &testInputFeed{}
	feed.appendUser("1 加 2")
	result, err := runtime.Run(ctx, RunRequest{RunID: "run", Assignment: Assignment{AgentName: "test"}, Models: fixedModels(chatModel), StreamID: "stream", OnStream: func(delta runstream.Delta) {
		mu.Lock()
		defer mu.Unlock()
		if applied, err := snapshot.Apply(delta); !applied || err != nil {
			t.Errorf("apply delta %#v: applied = %t, error = %v", delta, applied, err)
		}
		if midStream.StreamID == "" && snapshot.CandidateContent == "我来算" {
			midStream = snapshot.Clone()
			close(candidateStreamed)
		}
	}}, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "结果是 3" || len(result.Blocks) != 3 {
		t.Fatalf("result = %#v", result)
	}
	thinking, content, toolCall := result.Blocks[0], result.Blocks[1], result.Blocks[2]
	if thinking.Payload.Text != "先想想" || content.Payload.Text != "我来算" || toolCall.Payload.ToolCall.Arguments != `{"text":"3"}` || toolCall.Payload.ToolCall.Status != domain.AgentToolCallSucceeded {
		t.Fatalf("persisted blocks = %#v", result.Blocks)
	}
	if len(midStream.Blocks) != 1 || midStream.Blocks[0].ID != thinking.ID || midStream.Blocks[0].Text != "先想想" {
		t.Fatalf("mid-stream snapshot = %#v", midStream)
	}
	mu.Lock()
	defer mu.Unlock()
	if snapshot.CandidateContent != result.Content || len(snapshot.Blocks) != len(result.Blocks) {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
	for i, block := range snapshot.Blocks {
		if !reflect.DeepEqual(&block, result.Blocks[i].streamView()) {
			t.Fatalf("stream block %d = %#v, persisted = %#v", i, block, result.Blocks[i])
		}
	}
}

// TestRunCancellationKeepsPartialProcess 验证取消工具执行时不把取消作为可修正错误继续调用模型，并返回中断前已产生的过程内容。
func TestRunCancellationKeepsPartialProcess(t *testing.T) {
	runtime, err := newEchoRuntime()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	modelCalls := 0
	chatModel := &processChatModel{generate: func(context.Context, []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
		modelCalls++
		return assistantReply("准备计算", &schema.FunctionToolCall{CallID: "slow", Name: "echo", Arguments: `{"text":"3","delayMilliseconds":1000}`}), nil
	}}
	feed := &testInputFeed{}
	feed.appendUser("计算")
	result, err := runtime.Run(ctx, RunRequest{Assignment: Assignment{AgentName: "test"}, Models: fixedModels(chatModel), OnStream: func(delta runstream.Delta) {
		for _, operation := range delta.Operations {
			if operation.Block != nil && operation.Block.ToolCall != nil && operation.Block.ToolCall.Status == domain.AgentToolCallRunning {
				cancel()
			}
		}
	}}, feed)
	if err == nil || modelCalls != 1 || len(result.Blocks) == 0 || result.Content != "" {
		t.Fatalf("cancelled result = %#v, model calls = %d, error = %v", result, modelCalls, err)
	}
}
