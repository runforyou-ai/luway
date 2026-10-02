package agentruntime

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

type echoInput struct {
	Text              string `json:"text" jsonschema:"required" jsonschema_description:"Text to return"`
	DelayMilliseconds int    `json:"delayMilliseconds,omitempty" jsonschema_description:"Optional delay before returning the text"`
}

type echoOutput struct {
	Text string `json:"text"`
}

// newEchoTool 创建测试用回显 Tool，可按参数延时返回。
func newEchoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool("echo", "Return the given text.", echo)
}

// echo 按参数延时后原样返回文本，context 取消时返回取消错误。
func echo(ctx context.Context, input echoInput) (echoOutput, error) {
	if input.DelayMilliseconds < 0 {
		return echoOutput{}, errors.New("echo delay must not be negative")
	}
	if input.DelayMilliseconds > 0 {
		timer := time.NewTimer(time.Duration(input.DelayMilliseconds) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return echoOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
	return echoOutput{Text: input.Text}, nil
}

// newEchoRuntime 创建注册测试用回显 Tool 的 Eino Runtime。
func newEchoRuntime() (*EinoRuntime, error) {
	runtime, err := New()
	if err != nil {
		return nil, err
	}
	echoTool, err := newEchoTool()
	if err != nil {
		return nil, err
	}
	runtime.tools = []tool.BaseTool{echoTool}
	return runtime, nil
}
