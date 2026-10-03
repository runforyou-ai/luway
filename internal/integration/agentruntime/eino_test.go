package agentruntime

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// fixedModels 返回总是创建指定模型组件的工厂。
func fixedModels(chatModel model.AgenticModel) ModelFactory {
	return func(context.Context, ModelOptions) (model.AgenticModel, error) { return chatModel, nil }
}

type testInputFeed struct {
	mu       sync.Mutex
	desired  int64
	claimed  int64
	messages []Message
}

func (f *testInputFeed) Peek(_ context.Context, afterSeq int64) ([]Trigger, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]Trigger, 0)
	for seq := afterSeq + 1; seq <= f.desired; seq++ {
		result = append(result, Trigger{Seq: seq})
	}
	return result, nil
}

func (f *testInputFeed) Claim(_ context.Context, throughSeq int64) (ClaimedInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimed = min(throughSeq, f.desired)
	return ClaimedInput{Messages: append([]Message(nil), f.messages[:f.claimed]...), EndSeq: f.claimed}, nil
}

func (f *testInputFeed) appendUser(content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.desired++
	f.messages = append(f.messages, Message{ID: fmt.Sprint(f.desired), Role: MessageRoleUser, Content: content})
}

// assistantReply 构造带正文和工具调用块的模型输出。
func assistantReply(text string, calls ...*schema.FunctionToolCall) *schema.AgenticMessage {
	message := &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: text})}}
	for _, call := range calls {
		message.ContentBlocks = append(message.ContentBlocks, schema.NewContentBlock(call))
	}
	return message
}

// singleChunkStream 把一次完整模型输出包装为单分片流。
func singleChunkStream(message *schema.AgenticMessage, err error) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// withReasoning 在模型输出最前面加入思考块。
func withReasoning(message *schema.AgenticMessage, text string) *schema.AgenticMessage {
	message.ContentBlocks = append([]*schema.ContentBlock{schema.NewContentBlock(&schema.Reasoning{Text: text})}, message.ContentBlocks...)
	return message
}

// toolReply 构造框架返回给模型的工具结果消息。
func toolReply(callID, text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeUser, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.FunctionToolResult{
		CallID:  callID,
		Content: []*schema.FunctionToolResultContentBlock{{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: text}}},
	})}}
}

// toolResult 返回消息中的工具结果块，没有时返回 nil。
func toolResult(message *schema.AgenticMessage) *schema.FunctionToolResult {
	for _, block := range message.ContentBlocks {
		if block.Type == schema.ContentBlockTypeFunctionToolResult {
			return block.FunctionToolResult
		}
	}
	return nil
}

// messageText 拼接消息中的用户正文、模型正文和工具结果文本。
func messageText(message *schema.AgenticMessage) string {
	var text strings.Builder
	for _, block := range message.ContentBlocks {
		switch block.Type {
		case schema.ContentBlockTypeUserInputText:
			text.WriteString(block.UserInputText.Text)
		case schema.ContentBlockTypeAssistantGenText:
			text.WriteString(block.AssistantGenText.Text)
		case schema.ContentBlockTypeFunctionToolResult:
			for _, content := range block.FunctionToolResult.Content {
				if content.Type == schema.FunctionToolResultContentBlockTypeText {
					text.WriteString(content.Text.Text)
				}
			}
		}
	}
	return text.String()
}

// messageKind 区分系统、用户、模型输出和工具结果消息。
func messageKind(message *schema.AgenticMessage) string {
	if toolResult(message) != nil {
		return "tool"
	}
	return string(message.Role)
}

type steeringChatModel struct {
	mu               sync.Mutex
	calls            int
	calledWithoutNew bool
	firstCall        chan struct{}
	lastInput        []*schema.AgenticMessage
}

func (m *steeringChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastInput = input
	if m.calls == 1 {
		close(m.firstCall)
		return assistantReply("先计算一加二", &schema.FunctionToolCall{
			CallID: "echo-call-1", Name: "echo", Arguments: `{"text":"3","delayMilliseconds":500}`,
		}), nil
	}
	userMessages := 0
	for _, message := range input {
		if messageKind(message) == "user" {
			userMessages++
		}
	}
	if userMessages < 2 {
		m.calledWithoutNew = true
		return assistantReply("stale response"), nil
	}
	return assistantReply("response with follow-up"), nil
}

// Stream 以单个分片返回当前测试步骤的模型输出。
func (m *steeringChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

type cancelRaceInputFeed struct {
	mu                  sync.Mutex
	blockingPeekStarted chan struct{}
	blocked             bool
}

func (f *cancelRaceInputFeed) Peek(ctx context.Context, afterSeq int64) ([]Trigger, error) {
	if afterSeq == 0 {
		return []Trigger{{Seq: 1}}, nil
	}
	f.mu.Lock()
	if f.blocked {
		f.mu.Unlock()
		return nil, nil
	}
	f.blocked = true
	close(f.blockingPeekStarted)
	f.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *cancelRaceInputFeed) Claim(context.Context, int64) (ClaimedInput, error) {
	return ClaimedInput{Messages: []Message{{ID: "1", Role: MessageRoleUser, Content: "hello"}}, EndSeq: 1}, nil
}

type finalAfterWatcherModel struct {
	watcherStarted <-chan struct{}
}

func (m *finalAfterWatcherModel) Generate(ctx context.Context, _ []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.watcherStarted:
		return assistantReply("final response"), nil
	}
}

// Stream 以单个分片返回当前测试步骤的模型输出。
func (m *finalAfterWatcherModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestEinoRuntimeSteersBeforeNextModelCall 验证 Tool 完成后先吸收新输入再继续规划。
func TestEinoRuntimeSteersBeforeNextModelCall(t *testing.T) {
	var logOutput bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logOutput, nil)))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	echoTool, err := newEchoTool()
	if err != nil {
		t.Fatal(err)
	}
	chatModel := &steeringChatModel{firstCall: make(chan struct{})}
	runtime := &EinoRuntime{tools: []tool.BaseTool{echoTool}}
	feed := &testInputFeed{}
	feed.appendUser("echo three")

	go func() {
		<-chatModel.firstCall
		time.Sleep(50 * time.Millisecond)
		feed.appendUser("also include four")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{RunID: "test-run-id", Assignment: Assignment{AgentName: "test-agent"}, Models: fixedModels(chatModel), MaxTurns: 4}, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "response with follow-up" || result.EndSeq != 2 {
		t.Fatalf("result = %#v", result)
	}
	chatModel.mu.Lock()
	defer chatModel.mu.Unlock()
	if chatModel.calledWithoutNew {
		t.Fatal("model continued after tool call without the follow-up input")
	}
	if chatModel.calls != 2 {
		t.Fatalf("model calls = %d, want 2", chatModel.calls)
	}
	var kinds []string
	for _, message := range chatModel.lastInput {
		if message.Role != schema.AgenticRoleTypeSystem {
			kinds = append(kinds, messageKind(message))
		}
	}
	if !reflect.DeepEqual(kinds, []string{"user", "assistant", "tool", "user"}) {
		t.Fatalf("steered message kinds = %v", kinds)
	}
	messages := chatModel.lastInput[len(chatModel.lastInput)-4:]
	if messageText(messages[1]) != "先计算一加二" || toolCalls(messages[1])[0].CallID != "echo-call-1" ||
		toolResult(messages[2]).CallID != "echo-call-1" || messageText(messages[2]) != `{"text":"3"}` {
		t.Fatalf("steered tool exchange = %#v / %#v", messages[1], messages[2])
	}
	logs := logOutput.String()
	for _, expected := range []string{`"msg":"Agent Tool 调用开始"`, `"msg":"Agent Tool 调用成功"`, `"agent_run_id":"test-run-id"`, `"tool_name":"echo"`, `"tool_call_id":"echo-call-1"`, `"duration_ms":`} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("tool logs do not contain %q: %s", expected, logs)
		}
	}
	if strings.Contains(logs, "delayMilliseconds") || strings.Contains(logs, `"text":"3"`) || strings.Contains(logs, `"arguments"`) {
		t.Fatalf("tool logs contain call arguments: %s", logs)
	}
}

// TestEinoRuntimeIgnoresWatcherCancellationAfterSuccess 验证 watcher 协作取消后保留成功停止结果。
func TestEinoRuntimeIgnoresWatcherCancellationAfterSuccess(t *testing.T) {
	feed := &cancelRaceInputFeed{blockingPeekStarted: make(chan struct{})}
	chatModel := &finalAfterWatcherModel{watcherStarted: feed.blockingPeekStarted}
	runtime := &EinoRuntime{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{Assignment: Assignment{AgentName: "test-agent"}, Models: fixedModels(chatModel), MaxTurns: 2}, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final response" || result.EndSeq != 1 {
		t.Fatalf("result = %#v", result)
	}
}
