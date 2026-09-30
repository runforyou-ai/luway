package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

type toolCallContextKey struct{}

type toolCallMetadata struct {
	CallID string
}

// toolObserver 接收工具调用开始与结束的通知。
type toolObserver interface {
	// toolStarted 在工具开始执行时调用。
	toolStarted(input *compose.ToolInput, at time.Time) error
	// toolFinished 在工具执行结束时调用，err 为工具返回的错误。
	toolFinished(input *compose.ToolInput, at time.Time, result string, err error) error
}

// toolStarted 把工具调用标记为执行中。
func (r *processRecorder) toolStarted(input *compose.ToolInput, at time.Time) error {
	return r.updateTool(input.CallID, func(call *ToolCall) {
		call.Status, call.StartedAt = domain.AgentToolCallRunning, &at
	})
}

// toolFinished 记录工具调用的结果或错误，并清除子 Agent 活动。
func (r *processRecorder) toolFinished(input *compose.ToolInput, at time.Time, result string, err error) error {
	return r.updateTool(input.CallID, func(call *ToolCall) {
		call.CompletedAt, call.Activity = &at, ""
		if err != nil {
			message := err.Error()
			call.Status, call.Error = domain.AgentToolCallFailed, &message
		} else {
			call.Status, call.Result = domain.AgentToolCallSucceeded, &result
		}
	})
}

// toolExecutionMiddleware 通知观察者普通工具与多模态结果工具的执行过程，并把可继续处理的错误交回模型。
func toolExecutionMiddleware(observer toolObserver) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				var output *compose.ToolOutput
				message, err := recordToolCall(ctx, observer, input, func(ctx context.Context) (string, error) {
					var err error
					if output, err = next(ctx, input); err != nil {
						return "", err
					}
					return output.Result, nil
				})
				if message != "" {
					return &compose.ToolOutput{Result: message}, nil
				}
				return output, err
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				var output *compose.EnhancedInvokableToolOutput
				message, err := recordToolCall(ctx, observer, input, func(ctx context.Context) (string, error) {
					var err error
					if output, err = next(ctx, input); err != nil {
						return "", err
					}
					return toolResultSummary(output.Result), nil
				})
				if message != "" {
					return &compose.EnhancedInvokableToolOutput{Result: &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: message}}}}, nil
				}
				return output, err
			}
		},
	}
}

// recordToolCall 执行一次工具调用并通知观察者、记录日志；普通工具错误编码为交回模型的错误消息返回，执行取消和框架中断原样返回错误。
func recordToolCall(ctx context.Context, observer toolObserver, input *compose.ToolInput, call func(context.Context) (string, error)) (string, error) {
	startedAt := time.Now()
	if err := observer.toolStarted(input, startedAt); err != nil {
		return "", err
	}
	runID := runIDFromContext(ctx)
	slog.Info("Agent Tool 调用开始",
		"agent_run_id", runID,
		"tool_name", input.Name,
		"tool_call_id", input.CallID,
	)
	toolContext := context.WithValue(ctx, toolCallContextKey{}, toolCallMetadata{CallID: input.CallID})
	result, err := call(toolContext)
	if finishErr := observer.toolFinished(input, time.Now(), result, err); finishErr != nil {
		return "", finishErr
	}
	attributes := []any{
		"agent_run_id", runID,
		"tool_name", input.Name,
		"tool_call_id", input.CallID,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	}
	if err == nil {
		slog.Info("Agent Tool 调用成功", attributes...)
		return "", nil
	}
	attributes = append(attributes, "error", err)
	slog.Warn("Agent Tool 调用失败", attributes...)
	// 执行取消和框架中断继续向上收尾，普通工具错误成为模型输入。
	_, interrupted := compose.ExtractInterruptInfo(err)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || interrupted {
		return "", err
	}
	encoded, encodeErr := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
	if encodeErr != nil {
		return "", encodeErr
	}
	return string(encoded), nil
}

// toolResultSummary 把多模态工具结果转成过程记录中的文本，图片等媒体只记录类型。
func toolResultSummary(result *schema.ToolResult) string {
	if result == nil {
		return ""
	}
	parts := make([]string, 0, len(result.Parts))
	for _, part := range result.Parts {
		switch {
		case part.Type == schema.ToolPartTypeText:
			parts = append(parts, part.Text)
		case part.Image != nil:
			parts = append(parts, "[image "+part.Image.MIMEType+"]")
		default:
			parts = append(parts, "["+string(part.Type)+"]")
		}
	}
	return strings.Join(parts, "\n")
}
