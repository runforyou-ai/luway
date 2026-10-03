package appservice

// LocalComputerChangedEventName 是原生端本机电脑状态变化的 Wails 事件名，事件不携带数据，界面收到后重新读取本机电脑与本机环境。
const LocalComputerChangedEventName = "app:local-computer:changed"

// LocalComputer 定义本机在当前工作区的电脑注册状态与运行环境，电脑编号为空表示尚未注册；不作为电脑执行操作的平台运行环境为空。
type LocalComputer struct {
	ComputerID string          `json:"computerId"`
	Toolchain  *LocalToolchain `json:"toolchain"`
}

// LocalToolchainState 定义本机 Agent 运行环境的准备状态。
type LocalToolchainState string

const (
	// LocalToolchainStatePreparing 表示运行环境正在准备或尚未开始准备。
	LocalToolchainStatePreparing LocalToolchainState = "preparing"
	// LocalToolchainStateReady 表示已有可用的运行环境。
	LocalToolchainStateReady LocalToolchainState = "ready"
	// LocalToolchainStateFailed 表示最近一次准备失败，等待自动重试。
	LocalToolchainStateFailed LocalToolchainState = "failed"
	// LocalToolchainStateUninstalled 表示用户已卸载运行环境，重新安装前不自动安装。
	LocalToolchainStateUninstalled LocalToolchainState = "uninstalled"
)

// LocalToolchainFailure 定义运行环境准备失败的原因。
type LocalToolchainFailure string

const (
	// LocalToolchainFailureDownload 表示无法从下载源取得安装文件。
	LocalToolchainFailureDownload LocalToolchainFailure = "download"
	// LocalToolchainFailureVerify 表示下载的安装文件校验失败。
	LocalToolchainFailureVerify LocalToolchainFailure = "verify"
	// LocalToolchainFailureInstall 表示在本机安装失败。
	LocalToolchainFailureInstall LocalToolchainFailure = "install"
)

// LocalToolchain 定义本机 Agent 运行环境的准备状态，失败原因只在失败状态下非空，Updating 表示正在按用户请求更新。
type LocalToolchain struct {
	State    LocalToolchainState    `json:"state"`
	Failure  *LocalToolchainFailure `json:"failure"`
	Updating bool                   `json:"updating"`
}

// LocalEnvironment 定义本机为个人 AI 员工提供的运行环境、本地 MCP 服务与技能，未安装的组件版本为空。
type LocalEnvironment struct {
	Toolchain     LocalToolchain   `json:"toolchain"`
	Location      string           `json:"location"`
	UVVersion     string           `json:"uvVersion"`
	NodeVersion   string           `json:"nodeVersion"`
	PythonVersion string           `json:"pythonVersion"`
	MCPServers    []LocalMCPServer `json:"mcpServers"`
	Skills        []LocalSkill     `json:"skills"`
}

// LocalSkillSource 定义技能所在目录的来源。
type LocalSkillSource string

const (
	// LocalSkillSourceManaged 表示个人 AI 员工安装的技能，可以删除。
	LocalSkillSourceManaged LocalSkillSource = "managed"
	// LocalSkillSourceAgents 表示其他 AI 工具安装在跨工具共用目录中的技能。
	LocalSkillSourceAgents LocalSkillSource = "agents"
	// LocalSkillSourceClaude 表示 Claude 目录中的技能。
	LocalSkillSourceClaude LocalSkillSource = "claude"
)

// LocalSkill 定义这台电脑上负责人的个人 AI 员工共用的一个技能及其所在文件夹。
type LocalSkill struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Source      LocalSkillSource `json:"source"`
	Location    string           `json:"location"`
}

// LocalMCPServerType 定义本地 MCP 服务的连接方式。
type LocalMCPServerType string

const (
	// LocalMCPServerTypeStdio 表示启动本地进程并经标准输入输出通信。
	LocalMCPServerTypeStdio LocalMCPServerType = "stdio"
	// LocalMCPServerTypeSSE 表示连接 SSE 服务。
	LocalMCPServerTypeSSE LocalMCPServerType = "sse"
	// LocalMCPServerTypeHTTP 表示连接 Streamable HTTP 服务。
	LocalMCPServerTypeHTTP LocalMCPServerType = "http"
)

// LocalMCPServer 定义这台电脑上负责人的个人 AI 员工共用的一个本地 MCP 服务：本地进程给出启动命令与参数，SSE 与 Streamable HTTP 服务给出地址。
type LocalMCPServer struct {
	Name    string             `json:"name"`
	Type    LocalMCPServerType `json:"type"`
	Command string             `json:"command"`
	Args    []string           `json:"args"`
	URL     string             `json:"url"`
}

// LocalToolchainUpdate 定义更新本机运行环境的结果，Updated 表示有组件换了版本。
type LocalToolchainUpdate struct {
	Updated bool `json:"updated"`
}

// LocalMCPServerInput 定义添加到这台电脑的本地 MCP 服务：本地进程给出启动命令、参数与环境变量，SSE 与 Streamable HTTP 服务给出地址与请求头。
type LocalMCPServerInput struct {
	Name    string             `json:"name"`
	Type    LocalMCPServerType `json:"type"`
	Command string             `json:"command"`
	Args    []string           `json:"args"`
	Env     map[string]string  `json:"env"`
	URL     string             `json:"url"`
	Headers map[string]string  `json:"headers"`
}

// LocalSkillInstallInput 定义要安装到这台电脑的技能来源，来源含多个技能时按 Name 选择。
type LocalSkillInstallInput struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}
