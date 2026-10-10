package agentruntime

import (
	"context"

	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/luway/internal/agentcontract"
)

// GenerateObject 以模型参数关闭思考调用一次模型，要求输出 T 结构的 JSON 对象并解析，正文无法解析时带上解析错误重新请求一次；返回全部请求的累计用量，字段取值由调用方校验。
func GenerateObject[T any](ctx context.Context, model ModelConfig, instruction, input string) (T, agentcontract.Usage, error) {
	value, used, err := llm.GenerateObject[T](ctx, model.New, llm.GenerateRequest{
		Instruction: instruction, Input: input, MaxOutputTokens: model.MaxOutputTokens, Language: llm.Chinese,
	})
	return value, agentcontract.UsageFrom(used), err
}
