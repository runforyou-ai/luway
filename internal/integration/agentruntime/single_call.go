package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// SingleCallRequest 定义一次不注册工具、关闭思考的模型调用。
type SingleCallRequest struct {
	Instruction string
	Model       ModelConfig
	Input       string
}

// SingleCallResult 定义单次模型调用的正文与用量。
type SingleCallResult struct {
	Text  string
	Usage Usage
}

// SingleCaller 执行不产生运行记录的单次模型调用。
type SingleCaller interface {
	CallOnce(context.Context, SingleCallRequest) (SingleCallResult, error)
}

// CallOnce 关闭思考后以系统指令和一条用户消息调用一次模型，返回正文与用量。
func (r *EinoRuntime) CallOnce(ctx context.Context, request SingleCallRequest) (SingleCallResult, error) {
	config := request.Model
	config.DisableThinking = true
	chatModel, err := r.newModel(ctx, config)
	if err != nil {
		return SingleCallResult{}, err
	}
	message, err := chatModel.Generate(ctx, []*schema.AgenticMessage{
		schema.SystemAgenticMessage(request.Instruction), schema.UserAgenticMessage(request.Input),
	})
	if err != nil {
		return SingleCallResult{}, err
	}
	result := SingleCallResult{Text: assistantText(message)}
	if message.ResponseMeta != nil && message.ResponseMeta.TokenUsage != nil {
		result.Usage = Usage{
			PromptTokens:     message.ResponseMeta.TokenUsage.PromptTokens,
			CompletionTokens: message.ResponseMeta.TokenUsage.CompletionTokens,
			TotalTokens:      message.ResponseMeta.TokenUsage.TotalTokens,
		}
	}
	return result, nil
}

// DecodeJSONObject 把模型正文中从第一个 { 到最后一个 } 的 JSON 对象解析到 target，容许代码块包裹。
func DecodeJSONObject(text string, target any) error {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return errors.New("model response does not contain a JSON object")
	}
	return json.Unmarshal([]byte(text[start:end+1]), target)
}
