//go:build server

package server

// WithExecutionForTest 向包外集成测试提供任务执行上下文，不扩展生产接口。
var WithExecutionForTest = withExecution

// NewExecutionRuntimeForTest 复用当前工作区的测试数据库和任务运行时。
var NewExecutionRuntimeForTest = newEnqueueTestRuntime
