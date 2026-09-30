//go:build server

// Package agentrun 实现 Agent 会话触发、执行与持久账本。
package agentrun

const RunActionName = "agent.run"

// RunInput 定义 Agent Worker 任务输入。
type RunInput struct {
	RunID string `json:"run_id"`
}
