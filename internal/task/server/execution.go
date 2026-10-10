//go:build server

package server

import (
	"context"
	"errors"
)

// executionContextKey 是上下文中当前任务尝试的键。
type executionContextKey struct{}

// Execution 标识当前任务的一次执行尝试：TaskRunID 是任务编号，Attempt 是从 1 开始的尝试序号，InstanceID 是执行它的服务端实例；
// Finalizing 表示最终失败后的业务收尾。业务记录登记执行尝试后据此拒绝过期尝试的写回。
type Execution struct {
	TaskRunID  string
	Attempt    int
	InstanceID string
	Finalizing bool
}

// ErrExecutionLost 表示当前任务尝试已失去对业务记录的执行权。
var ErrExecutionLost = errors.New("task execution no longer owns the record")

// withExecution 把当前任务尝试写入上下文。
func withExecution(ctx context.Context, execution Execution) context.Context {
	return context.WithValue(ctx, executionContextKey{}, execution)
}

// CurrentExecution 返回任务调用携带的当前尝试，直接 Action 调用不携带。
func CurrentExecution(ctx context.Context) (Execution, bool) {
	execution, ok := ctx.Value(executionContextKey{}).(Execution)
	return execution, ok
}
