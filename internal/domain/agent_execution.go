package domain

// AgentExecutionMode 表示 AI 员工的执行方式。
type AgentExecutionMode string

const (
	// AgentExecutionModeManaged 表示由应用内置的运行时使用企业模型服务执行。
	AgentExecutionModeManaged AgentExecutionMode = "managed"
	// AgentExecutionModeLocalAgent 表示由个人 AI 员工绑定电脑上的本机 Agent 执行，只用于个人 AI 员工。
	AgentExecutionModeLocalAgent AgentExecutionMode = "local_agent"
)

// LocalAgentKind 表示经 ACP 驱动的本机 Agent 种类。
type LocalAgentKind string

const (
	LocalAgentKindCodex LocalAgentKind = "codex"
)

// LocalAgentKindValid 判断本机 Agent 种类是否受支持。
func LocalAgentKindValid(kind LocalAgentKind) bool {
	return kind == LocalAgentKindCodex
}

// AgentMediaMaxBytes 是单个附件随模型请求直传的字节上限，超出时只保留正文中的链接。
const AgentMediaMaxBytes = 10 << 20
