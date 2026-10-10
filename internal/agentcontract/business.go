package agentcontract

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
)

// BusinessCaller 是一个业务系统在本次运行内的调用会话，由执行侧按传输方式提供，运行结束时关闭。
type BusinessCaller interface {
	// Call 按工具目录中的原工具名调用工具，参数已写入绑定值；工具自身报告的失败作为错误返回。
	Call(ctx context.Context, tool string, arguments json.RawMessage) (string, error)
	// Close 释放会话持有的连接。
	Close() error
}

// BusinessSystem 定义本次运行挂载的一个业务系统：挂载的工具与调用会话。
type BusinessSystem struct {
	ID     string
	Name   string
	Tools  []BusinessToolMount
	Caller BusinessCaller
}

// BusinessToolMount 定义一个业务系统工具在本次运行中的挂载方式：取自保存的工具目录的定义、服务端填入的绑定参数、是否只读、操作级别与执行前需要的人工介入。
type BusinessToolMount struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Bound       map[string]string // 参数名到服务端填入的值，这些参数不出现在模型可见的参数定义中。
	ReadOnly    bool              // 只读工具可重新执行且没有外部副作用。
	ToolPolicy
}

// BindArguments 把服务端绑定的参数值写入模型给出的参数对象，同名参数以绑定值为准。
func BindArguments(arguments json.RawMessage, bound map[string]string) (json.RawMessage, error) {
	if len(bound) == 0 {
		return arguments, nil
	}
	values := map[string]json.RawMessage{}
	if len(arguments) > 0 && string(arguments) != "null" {
		if err := json.Unmarshal(arguments, &values); err != nil {
			return nil, errors.New("参数不是 JSON 对象，请重新提交。")
		}
	}
	for name, value := range bound {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		values[name] = encoded
	}
	return json.Marshal(values)
}

// ToolPolicy 是工具在本次运行中的操作级别与执行前需要的人工介入。
type ToolPolicy struct {
	Level domain.OperationLevel
	// Intervention 不为空时调用不执行，提交给成员确认或审批，模型收到已提交的结果。
	Intervention domain.ToolIntervention
}
