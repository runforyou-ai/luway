//go:build server

package agentrun

// RunActionName 是执行一次 Agent 运行的任务名称。
const RunActionName = "agent.run"

// RunInput 定义 Agent Worker 任务输入。
type RunInput struct {
	RunID string `json:"run_id"`
}
