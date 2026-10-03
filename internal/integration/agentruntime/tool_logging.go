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
	"github.com/runforyou-ai/luway/internal/domain"
)

type toolCallContextKey struct{}

// awaitingResult 是等待外部结果的调用在本批工具中的占位结果。
const awaitingResult = `{"status":"awaiting_external_result"}`

type toolCallMetadata struct {
	CallID string
}

// toolObserver 接收工具调用开始与结束的通知。
type toolObserver interface {
	// toolStarted 在工具开始执行时调用。
	toolStarted(ctx context.Context, input *compose.ToolInput, at time.Time) error
	// toolFinished 在工具执行结束时调用，err 为工具返回的错误，等待外部结果时为 ErrAwaitExternal。
	toolFinished(ctx context.Context, input *compose.ToolInput, at time.Time, result string, err error) error
}

// toolStarted 把工具调用标记为执行中。
func (r *processRecorder) toolStarted(ctx context.Context, input *compose.ToolInput, at time.Time) error {
	return r.updateTool(ctx, input.CallID, func(call *ToolCall) {
		call.Status, call.StartedAt = domain.AgentToolCallRunning, &at
	})
}

// toolFinished 记录工具调用的结果、错误或等待外部结果并清除子 Agent 活动，随后保存工具改动的执行期状态。
func (r *processRecorder) toolFinished(ctx context.Context, input *compose.ToolInput, at time.Time, result string, err error) error {
	if err := r.updateTool(ctx, input.CallID, func(call *ToolCall) {
		call.Activity = ""
		settleToolCall(call, at, result, err)
	}); err != nil {
		return err
	}
	if r.onToolFinished == nil {
		return nil
	}
	return r.onToolFinished(ctx)
}

// settleToolCall 按工具返回值填写调用状态：等待外部结果时只改为等待，其余情况记录结束时间与结果或错误。
func settleToolCall(call *ToolCall, at time.Time, result string, err error) {
	switch {
	case errors.Is(err, ErrAwaitExternal):
		call.Status = domain.AgentToolCallWaiting
	case err != nil:
		message := err.Error()
		call.Status, call.Error, call.CompletedAt = domain.AgentToolCallFailed, &message, &at
	default:
		call.Status, call.Result, call.CompletedAt = domain.AgentToolCallSucceeded, &result, &at
	}
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
	if err := observer.toolStarted(ctx, input, startedAt); err != nil {
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
	if finishErr := observer.toolFinished(ctx, input, time.Now(), result, err); finishErr != nil {
		return "", finishErr
	}
	// 主 Agent 等待外部结果的调用先交给模型一条占位结果，运行在本批工具结束后挂起，恢复时替换为实际结果。
	if _, main := observer.(*processRecorder); main && errors.Is(err, ErrAwaitExternal) {
		slog.Info("Agent Tool 调用等待外部结果", "agent_run_id", runIDFromContext(ctx), "tool_name", input.Name, "tool_call_id", input.CallID)
		return awaitingResult, nil
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
