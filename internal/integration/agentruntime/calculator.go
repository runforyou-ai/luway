package agentruntime

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

type calculatorInput struct {
	Operation         string  `json:"operation" jsonschema:"required,enum=add,enum=subtract,enum=multiply,enum=divide" jsonschema_description:"Arithmetic operation to perform"`
	Left              float64 `json:"left" jsonschema:"required" jsonschema_description:"Left operand"`
	Right             float64 `json:"right" jsonschema:"required" jsonschema_description:"Right operand"`
	DelayMilliseconds int     `json:"delayMilliseconds,omitempty" jsonschema_description:"Optional concurrency-test delay before returning the result"`
}

type calculatorOutput struct {
	Result float64 `json:"result"`
}

// newCalculatorTool 创建四则运算 Tool。
func newCalculatorTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"calculator",
		"Perform addition, subtraction, multiplication, or division on two numbers.",
		calculate,
	)
}

const calculatorMaxDelay = 30 * time.Second

// calculate 执行可延时的四则运算并拒绝无效结果。
func calculate(ctx context.Context, input calculatorInput) (calculatorOutput, error) {
	delay := time.Duration(input.DelayMilliseconds) * time.Millisecond
	if input.DelayMilliseconds < 0 || delay > calculatorMaxDelay {
		return calculatorOutput{}, errors.New("calculator delay must be between 0 and 30000 milliseconds")
	}
	// 读取当前 Tool 实现可用于安全日志的调用标识。
	toolCall, _ := ctx.Value(toolCallContextKey{}).(toolCallMetadata)
	slog.Info("Calculator Tool 执行配置",
		"agent_run_id", runIDFromContext(ctx),
		"tool_call_id", toolCall.CallID,
		"operation", input.Operation,
		"delay_ms", input.DelayMilliseconds,
	)
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return calculatorOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
	var result float64
	switch input.Operation {
	case "add":
		result = input.Left + input.Right
	case "subtract":
		result = input.Left - input.Right
	case "multiply":
		result = input.Left * input.Right
	case "divide":
		if input.Right == 0 {
			return calculatorOutput{}, errors.New("cannot divide by zero")
		}
		result = input.Left / input.Right
	default:
		return calculatorOutput{}, errors.New("unsupported calculator operation")
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return calculatorOutput{}, errors.New("calculator result is not finite")
	}
	return calculatorOutput{Result: result}, nil
}
