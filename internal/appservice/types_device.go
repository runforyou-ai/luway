package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// DevicePlatform 定义注册设备的运行平台。
type DevicePlatform string

const (
	DevicePlatformMacOS   DevicePlatform = DevicePlatform(domain.DevicePlatformMacOS)
	DevicePlatformWindows DevicePlatform = DevicePlatform(domain.DevicePlatformWindows)
	DevicePlatformLinux   DevicePlatform = DevicePlatform(domain.DevicePlatformLinux)
)

// Device 定义成员注册到企业的本机设备，LocalAgents 是设备上报的已安装且可用的本机 Agent。
type Device struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Platform    DevicePlatform   `json:"platform"`
	LocalAgents []LocalAgentKind `json:"localAgents"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

// DeviceRegistrationInput 定义设备注册上报的本机信息。
type DeviceRegistrationInput struct {
	InstallID string         `json:"installId"`
	Name      string         `json:"name"`
	Platform  DevicePlatform `json:"platform"`
}

// DeviceList 定义当前用户的设备列表。
type DeviceList struct {
	Devices []Device `json:"devices"`
}

// LocalDeviceChangedEventName 是原生端本机设备状态变化的 Wails 事件名，事件不携带数据，界面收到后重新读取本机设备。
const LocalDeviceChangedEventName = "app:local-device:changed"

// LocalDevice 定义本机在当前企业服务器上的设备注册状态与 Agent 运行环境，设备编号为空表示尚未注册；不执行 Agent 运行的平台运行环境为空。
type LocalDevice struct {
	DeviceID  string          `json:"deviceId"`
	Toolchain *LocalToolchain `json:"toolchain"`
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
