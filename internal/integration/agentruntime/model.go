package agentruntime

import (
	"context"
	"slices"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// toolArgumentsNormalizer 把工具调用的空参数补为空 JSON 对象，请求体按 omitempty 序列化时保留 arguments 字段。
type toolArgumentsNormalizer struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
}

// BeforeModelRewriteState 在每次模型调用前替换空工具参数，含空参数的消息及其全部工具调用块复制后再改写。
func (*toolArgumentsNormalizer) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	for i, message := range state.Messages {
		if !slices.ContainsFunc(message.ContentBlocks, func(block *schema.ContentBlock) bool {
			return block.Type == schema.ContentBlockTypeFunctionToolCall && block.FunctionToolCall.Arguments == ""
		}) {
			continue
		}
		normalized := *message
		normalized.ContentBlocks = slices.Clone(message.ContentBlocks)
		for j, block := range normalized.ContentBlocks {
			if block.Type != schema.ContentBlockTypeFunctionToolCall {
				continue
			}
			call := *block.FunctionToolCall
			if call.Arguments == "" {
				call.Arguments = "{}"
			}
			filled := *block
			filled.FunctionToolCall = &call
			normalized.ContentBlocks[j] = &filled
		}
		state.Messages[i] = &normalized
	}
	return ctx, state, nil
}
