package agentruntime

import (
	"context"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestTurnHistoryRetainsOutputsAcrossSlidingInput 验证滑动历史和重复正文下执行上下文的完整性与去重。
func TestTurnHistoryRetainsOutputsAcrossSlidingInput(t *testing.T) {
	history := &turnHistory{}
	first := Message{ID: "1", Role: MessageRoleUser, Content: "继续"}
	second := Message{ID: "2", Role: MessageRoleUser, Content: "继续"}
	history.appendInput(context.Background(), []Message{first}, mediaInput{})
	call := withReasoning(assistantReply("先查资料", &schema.FunctionToolCall{CallID: "lookup", Name: "search", Arguments: "{}"}), "需要查证")
	result := toolReply("lookup", "找到规定")
	history.appendOutput([]*schema.AgenticMessage{call, result})
	history.appendInput(context.Background(), []Message{first, second}, mediaInput{})
	history.appendOutput([]*schema.AgenticMessage{assistantReply("已查到差旅规定")})
	got := history.appendInput(context.Background(), []Message{second, {ID: "3", Role: MessageRoleUser, Content: "用中文"}}, mediaInput{})
	want := []*schema.AgenticMessage{schema.UserAgenticMessage("继续"), call, result, schema.UserAgenticMessage("继续"), assistantReply("已查到差旅规定"), schema.UserAgenticMessage("用中文")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

// TestTurnHistoryDropsEmptyAssistantOutputs 验证 assistant 历史保留有效正文或工具调用，工具历史保留空结果。
func TestTurnHistoryDropsEmptyAssistantOutputs(t *testing.T) {
	history := &turnHistory{}
	reasoning := withReasoning(assistantReply(""), "还在思考")
	completed := withReasoning(assistantReply("", &schema.FunctionToolCall{CallID: "done"}), "工具已返回")
	result := toolReply("done", "")
	history.appendOutput([]*schema.AgenticMessage{assistantReply(""), reasoning, completed, result})
	if !reflect.DeepEqual(history.messages, []*schema.AgenticMessage{completed, result}) {
		t.Fatalf("retained output = %#v", history.messages)
	}
}

// TestTurnHistoryKeepsUnansweredCalls 验证抢占后没有结果的工具调用原样留在历史中，由修补中间件在调用模型前补上结果。
func TestTurnHistoryKeepsUnansweredCalls(t *testing.T) {
	history := &turnHistory{}
	call := assistantReply("准备查询", &schema.FunctionToolCall{CallID: "done"}, &schema.FunctionToolCall{CallID: "skipped"})
	failed := toolReply("done", `{"error":"查询失败"}`)
	skipped := withReasoning(assistantReply("", &schema.FunctionToolCall{CallID: "not-started"}), "准备调用工具")
	history.appendOutput([]*schema.AgenticMessage{call, failed, skipped})
	if !reflect.DeepEqual(history.messages, []*schema.AgenticMessage{call, failed, skipped}) {
		t.Fatalf("retained output = %#v", history.messages)
	}
}

// TestTurnHistoryReappendsRevisedMessage 验证同一消息在修订变化后重新进入历史，修订未变时仍按编号去重。
func TestTurnHistoryReappendsRevisedMessage(t *testing.T) {
	history := &turnHistory{}
	pending := Message{ID: "1", Revision: "pending", Role: MessageRoleUser, Content: "照片接收中"}
	history.appendInput(context.Background(), []Message{pending}, mediaInput{})
	history.appendOutput([]*schema.AgenticMessage{assistantReply("请稍等")})
	ready := Message{ID: "1", Revision: "ready", Role: MessageRoleUser, Content: "照片已就绪"}
	got := history.appendInput(context.Background(), []Message{pending, ready}, mediaInput{})
	want := []*schema.AgenticMessage{schema.UserAgenticMessage("照片接收中"), assistantReply("请稍等"), schema.UserAgenticMessage("照片已就绪")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}
