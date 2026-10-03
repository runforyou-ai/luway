package agentruntime

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// TestToolArgumentsNormalizerFillsEmptyArguments 验证空工具参数补为空 JSON 对象且不修改原消息。
func TestToolArgumentsNormalizerFillsEmptyArguments(t *testing.T) {
	original := assistantReply("",
		&schema.FunctionToolCall{CallID: "empty-call", Name: "list_items"},
		&schema.FunctionToolCall{CallID: "filled-call", Name: "echo", Arguments: `{"text":"1"}`},
	)
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: []*schema.AgenticMessage{schema.UserAgenticMessage("问题"), original}}
	_, state, err := (&toolArgumentsNormalizer{}).BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := toolCalls(state.Messages[1])
	if calls[0].Arguments != "{}" || calls[1].Arguments != `{"text":"1"}` {
		t.Fatalf("tool calls = %#v", calls)
	}
	if toolCalls(original)[0].Arguments != "" {
		t.Fatalf("原消息被修改：%#v", toolCalls(original))
	}
}
