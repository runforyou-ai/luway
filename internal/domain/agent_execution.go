package domain

// AgentExecutionMode 表示 AI 员工的执行方式。
type AgentExecutionMode string

const (
	// AgentExecutionModeManaged 表示由应用内置的运行时使用企业模型服务执行。
	AgentExecutionModeManaged AgentExecutionMode = "managed"
)

// AgentMediaMaxBytes 是单个附件随模型请求直传的字节上限，超出时只保留正文中的链接。
const AgentMediaMaxBytes = 10 << 20
