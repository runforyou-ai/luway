package native

import "strconv"

// NotificationPermissionStatus 表示当前设备的系统通知授权状态。
type NotificationPermissionStatus string

const (
	NotificationPermissionStatusPrompt      NotificationPermissionStatus = "prompt"
	NotificationPermissionStatusGranted     NotificationPermissionStatus = "granted"
	NotificationPermissionStatusDenied      NotificationPermissionStatus = "denied"
	NotificationPermissionStatusUnsupported NotificationPermissionStatus = "unsupported"
)

// ClientUpdateState 表示原生端从当前服务器更新客户端的状态。
type ClientUpdateState string

const (
	// ClientUpdateStateUnsupported 表示当前端不能从服务器更新自身。
	ClientUpdateStateUnsupported ClientUpdateState = "unsupported"
	// ClientUpdateStateCurrent 表示服务器没有提供比当前客户端更新的版本。
	ClientUpdateStateCurrent ClientUpdateState = "current"
	// ClientUpdateStateReady 表示新版本已下载并通过签名校验，重启后生效。
	ClientUpdateStateReady ClientUpdateState = "ready"
	// ClientUpdateStateAvailable 表示服务器提供更新的版本，但当前端不能替换自身，需从服务器下载页安装。
	ClientUpdateStateAvailable ClientUpdateState = "available"
)

// ClientUpdate 是原生端从当前服务器更新客户端的结果。
type ClientUpdate struct {
	State ClientUpdateState `json:"state"`
	// Version 是服务器提供的新版本，只在 State 为 ready 或 available 时有值。
	Version string `json:"version"`
}

// ConversationWindowInput 定义桌面端打开会话独立窗口的输入，WorkspaceSlug 是会话所在工作区的标识。
type ConversationWindowInput struct {
	WorkspaceSlug  string `json:"workspaceSlug"`
	ConversationID string `json:"conversationId"`
	Title          string `json:"title"`
}

// MessageNotificationInput 定义当前设备的新消息通知内容；Path 是点击通知后打开的工作区页面地址（`/w/<工作区标识>/...`），为空时只把应用带到前台。
type MessageNotificationInput struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	SoundEnabled bool   `json:"soundEnabled"`
	Path         string `json:"path"`
}

// NotificationOpenedEventName 是原生端通知被点击后通知界面的 Wails 事件名，事件不携带数据，主界面收到后读取并清除待打开的页面地址。
const NotificationOpenedEventName = "app:notification:opened"

// ServerLinkOpenedEventName 是原生端被连接链接唤起后通知界面的 Wails 事件名，事件不携带数据，主界面收到后读取并清除链接携带的部署地址。
const ServerLinkOpenedEventName = "app:server-link:opened"

// ImageFile 定义原生端选择的图片文件。
type ImageFile struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	DataBase64  string `json:"dataBase64"`
}

// TextFileInput 定义原生端保存的文本文件：Name 为建议文件名，Content 为 UTF-8 文本内容。
type TextFileInput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

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

const maxDisplayedUnreadCount = 99

// UnreadIndicatorState 定义未读数量和托盘提醒条件。
type UnreadIndicatorState struct {
	Count            int  `json:"count"`
	AttentionEnabled bool `json:"attentionEnabled"`
	AttentionPending bool `json:"attentionPending"`
}

// UnreadIndicator 更新当前平台的未读提示。
type UnreadIndicator interface {
	SetUnreadState(state UnreadIndicatorState) error
}

// FormatUnreadCount 将未读消息数格式化为适合原生角标展示的短文本。
func FormatUnreadCount(count int) string {
	if count <= 0 {
		return ""
	}
	if count > maxDisplayedUnreadCount {
		return "99+"
	}
	return strconv.Itoa(count)
}

// ComputerIdentity 定义本机执行器安装标识与上报的电脑名称，前端以之为各工作区注册这台电脑；本机不作为电脑时为空。
type ComputerIdentity struct {
	InstallID string `json:"installId"`
	Name      string `json:"name"`
}

// ComputerAccount 定义一台服务器上的一个账号，本机电脑注册按它分组。
type ComputerAccount struct {
	ServerURL string `json:"serverUrl"`
	AccountID string `json:"accountId"`
}

// AttachedComputer 定义本机为一个账号在一个工作区注册的电脑。
type AttachedComputer struct {
	WorkspaceID string `json:"workspaceId"`
	ComputerID  string `json:"computerId"`
}

// ComputerAttachment 定义前端为一个账号在一个工作区注册这台电脑得到的电脑凭据。
type ComputerAttachment struct {
	ServerURL   string `json:"serverUrl"`
	AccountID   string `json:"accountId"`
	WorkspaceID string `json:"workspaceId"`
	ComputerID  string `json:"computerId"`
	Credential  string `json:"credential"`
}
