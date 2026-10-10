package domain

import (
	"encoding/json"
	"time"
)

const (
	// ComputerPresenceTimeout 是电脑两次已认证 HTTP 心跳的最长间隔，超过即视为离线。
	ComputerPresenceTimeout = time.Minute
	// ComputerOperationTimeout 是电脑执行单次操作的时限，执行器到时终止操作；委派本机 Agent 的一轮不设时限。
	ComputerOperationTimeout = 10 * time.Minute
)

// ExecutorVersion 是执行器与服务端约定的操作原语版本，新增操作原语或操作约束时加一；服务端只接受与自身一致的执行器。
const ExecutorVersion = "5"

// ComputerKind 定义电脑的归属类型。
type ComputerKind string

const (
	// ComputerKindPersonal 表示成员的个人电脑，只为该成员的个人 AI 员工执行操作。
	ComputerKindPersonal ComputerKind = "personal"
	// ComputerKindWorkspace 表示工作区电脑，为绑定它的服务型 AI 员工执行操作。
	ComputerKindWorkspace ComputerKind = "workspace"
)

// ComputerPlatform 定义电脑的操作系统平台。
type ComputerPlatform string

const (
	ComputerPlatformMacOS   ComputerPlatform = "macos"
	ComputerPlatformWindows ComputerPlatform = "windows"
	ComputerPlatformLinux   ComputerPlatform = "linux"
)

// ValidComputerPlatform 判断电脑平台是否为已知取值。
func ValidComputerPlatform(platform ComputerPlatform) bool {
	switch platform {
	case ComputerPlatformMacOS, ComputerPlatformWindows, ComputerPlatformLinux:
		return true
	}
	return false
}

// ComputerCapabilities 是执行器上报的执行能力。
type ComputerCapabilities struct {
	// Shell 是命令使用的解释器名称，如 bash、PowerShell。
	Shell string `json:"shell"`
	// ManagedToolchain 表示命令可以直接使用托管的 uv、Node.js 与 Python。
	ManagedToolchain bool `json:"managedToolchain"`
	// FolderRoot 是各会话默认文件夹的上级目录。
	FolderRoot string `json:"folderRoot"`
	// MCPServers 是电脑上已添加且能读取工具目录的本机 MCP 服务，按名称排序。
	MCPServers []ComputerMCPServer `json:"mcpServers"`
	// Skills 是电脑上可用的技能，按名称排序。
	Skills []ComputerSkill `json:"skills"`
	// Browser 表示电脑可以执行浏览器操作。
	Browser bool `json:"browser"`
	// Desktop 表示电脑可以执行桌面操作。
	Desktop bool `json:"desktop"`
	// LocalAgents 是电脑上可以接受委派的本机 Agent，按名称排序。
	LocalAgents []ComputerLocalAgent `json:"localAgents"`
}

// ComputerLocalAgent 是电脑上一个经 ACP 协议接入的本机 Agent 的名称与用途说明。
type ComputerLocalAgent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ComputerMCPServer 是电脑上一个本机 MCP 服务及其工具目录。
type ComputerMCPServer struct {
	Name  string            `json:"name"`
	Tools []ComputerMCPTool `json:"tools"`
}

// ComputerMCPTool 是本机 MCP 服务提供的一个工具。
type ComputerMCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// ReadOnlyHint 与 DestructiveHint 是服务声明的只读与不可撤销提示，未声明时为空。
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
}

// ComputerSkill 是电脑上一个可用技能的名称、简介与所在文件夹。
type ComputerSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Dir         string `json:"dir"`
	// Fork 表示技能元数据声明交给独立上下文的子 Agent 执行。
	Fork bool `json:"fork,omitempty"`
}

// ComputerOperationKind 定义派发给电脑的操作原语。
type ComputerOperationKind string

const (
	// ComputerOperationReadFile 读取文本文件。
	ComputerOperationReadFile ComputerOperationKind = "read_file"
	// ComputerOperationWriteFile 以完整内容创建或覆盖文件。
	ComputerOperationWriteFile ComputerOperationKind = "write_file"
	// ComputerOperationEditFile 把文件中的一段原文替换为新内容。
	ComputerOperationEditFile ComputerOperationKind = "edit_file"
	// ComputerOperationCommand 执行一条命令。
	ComputerOperationCommand ComputerOperationKind = "command"
	// ComputerOperationMCPCall 调用本机 MCP 服务的工具。
	ComputerOperationMCPCall ComputerOperationKind = "mcp_call"
	// ComputerOperationLoadSkill 读取技能说明与附带文件清单。
	ComputerOperationLoadSkill ComputerOperationKind = "load_skill"
	// ComputerOperationBrowser 在电脑的浏览器中执行一个动作，每个会话使用独立的浏览器上下文。
	ComputerOperationBrowser ComputerOperationKind = "browser"
	// ComputerOperationDesktop 在电脑桌面上执行一个动作，同一台电脑同一时刻只执行一个桌面动作。
	ComputerOperationDesktop ComputerOperationKind = "desktop"
	// ComputerOperationLocalAgent 把一轮任务交给本机 Agent，在本机 Agent 会话中执行，同一会话的各轮依次执行。
	ComputerOperationLocalAgent ComputerOperationKind = "local_agent"
)

// SyncsSharedFiles 判断操作在会话文件夹中运行第三方进程、执行前后同步共享文件区副本：命令与委派本机 Agent 的一轮。
func (k ComputerOperationKind) SyncsSharedFiles() bool {
	return k == ComputerOperationCommand || k == ComputerOperationLocalAgent
}

// BrowserAction 定义浏览器操作的动作。
type BrowserAction string

const (
	// BrowserActionNavigate 打开网址。
	BrowserActionNavigate BrowserAction = "navigate"
	// BrowserActionSnapshot 读取当前页面的可访问性结构。
	BrowserActionSnapshot BrowserAction = "snapshot"
	// BrowserActionClick 点击页面元素。
	BrowserActionClick BrowserAction = "click"
	// BrowserActionType 在页面元素中输入文本。
	BrowserActionType BrowserAction = "type"
	// BrowserActionPressKey 按下键盘按键。
	BrowserActionPressKey BrowserAction = "press_key"
	// BrowserActionScroll 滚动页面。
	BrowserActionScroll BrowserAction = "scroll"
	// BrowserActionScreenshot 截取当前页面。
	BrowserActionScreenshot BrowserAction = "screenshot"
)

// BrowserActions 是浏览器操作支持的全部动作。
var BrowserActions = []BrowserAction{
	BrowserActionNavigate, BrowserActionSnapshot, BrowserActionClick, BrowserActionType, BrowserActionPressKey, BrowserActionScroll, BrowserActionScreenshot,
}

// DesktopAction 定义桌面操作的动作。
type DesktopAction string

const (
	// DesktopActionScreenshot 截取屏幕。
	DesktopActionScreenshot DesktopAction = "screenshot"
	// DesktopActionClick 在屏幕坐标处点击。
	DesktopActionClick DesktopAction = "click"
	// DesktopActionDoubleClick 在屏幕坐标处双击。
	DesktopActionDoubleClick DesktopAction = "double_click"
	// DesktopActionDrag 从一个屏幕坐标拖动到另一个。
	DesktopActionDrag DesktopAction = "drag"
	// DesktopActionType 输入文本。
	DesktopActionType DesktopAction = "type"
	// DesktopActionPressKey 按下键盘按键或组合键。
	DesktopActionPressKey DesktopAction = "press_key"
	// DesktopActionScroll 在屏幕坐标处滚动。
	DesktopActionScroll DesktopAction = "scroll"
)

// DesktopActions 是桌面操作支持的全部动作。
var DesktopActions = []DesktopAction{
	DesktopActionScreenshot, DesktopActionClick, DesktopActionDoubleClick, DesktopActionDrag, DesktopActionType, DesktopActionPressKey, DesktopActionScroll,
}

// ComputerOperation 是派发给电脑的一次操作；相对路径与命令工作目录以会话默认文件夹为起点。
type ComputerOperation struct {
	Kind ComputerOperationKind `json:"kind"`
	// Folder 是会话默认文件夹在执行器会话文件夹根目录下的名称。
	Folder string `json:"folder"`
	// Confined 表示读取文件只能访问默认文件夹与电脑上的技能文件夹，写入与修改文件只能访问默认文件夹。
	Confined bool `json:"confined,omitempty"`
	// Path 是文件操作的路径：绝对路径、~ 开头的路径或相对默认文件夹的路径。
	Path   string `json:"path,omitempty"`
	Offset int    `json:"offset,omitempty"` // 读取起始行，从 1 开始，零值从第一行读取。
	Limit  int    `json:"limit,omitempty"`  // 读取行数上限，零值使用默认上限。
	// Content 是写入文件的完整内容。
	Content    string `json:"content,omitempty"`
	OldString  string `json:"oldString,omitempty"`
	NewString  string `json:"newString,omitempty"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
	// BaseHash 是写入或编辑已有文件时要求的当前内容摘要，与文件实际内容不一致时操作以冲突失败。
	BaseHash  string `json:"baseHash,omitempty"`
	Command   string `json:"command,omitempty"`
	MCPServer string `json:"mcpServer,omitempty"`
	MCPTool   string `json:"mcpTool,omitempty"`
	// Action 是浏览器或桌面操作的动作，取值为 BrowserAction 或 DesktopAction。
	Action string `json:"action,omitempty"`
	// Arguments 是 MCP 工具或浏览器、桌面动作参数的 JSON 对象。
	Arguments string `json:"arguments,omitempty"`
	Skill     string `json:"skill,omitempty"`
	// LocalAgent 是本机 Agent 名称，Session 是这一轮所属的本机 Agent 会话编号，AgentSession 是领取时会话中记录的本机 Agent 最近返回的 ACP 会话编号，Prompt 是交给本机 Agent 的提示。
	LocalAgent   string `json:"localAgent,omitempty"`
	Session      string `json:"session,omitempty"`
	AgentSession string `json:"agentSession,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	// NewSession 表示这一轮开启新的本机 Agent 会话，释放会话中原有的会话。
	NewSession bool `json:"newSession,omitempty"`
}

// ComputerOutcome 是电脑上报的操作结果。
type ComputerOutcome struct {
	// Output 是交给模型的结果正文；技能操作为技能说明正文。
	Output string `json:"output"`
	// Error 非空表示操作失败，内容交给模型。
	Error string `json:"error,omitempty"`
	// Path 是文件与技能操作解析后的绝对路径。
	Path string `json:"path,omitempty"`
	// Hash 是文件操作完成后文件内容的 SHA-256 摘要。
	Hash string `json:"hash,omitempty"`
	// Files 是技能文件夹中附带的文件，相对技能文件夹。
	Files []string `json:"files,omitempty"`
	// Aborted 表示操作按服务端的中止要求提前结束，实际结果未知。
	Aborted bool `json:"aborted,omitempty"`
	// AgentSession 是本机 Agent 执行这一轮使用的 ACP 会话编号。
	AgentSession string `json:"agentSession,omitempty"`
	// SharedWrites 是执行前后同步时写回会话共享文件区的文件。
	SharedWrites []SharedFileWrite `json:"sharedWrites,omitempty"`
}

// SharedFileWrite 是电脑同步时写回会话共享文件区的一个文件：Path 是共享文件区内的原路径，SavedAs 是实际写入的路径，另存为冲突副本时与 Path 不同。
type SharedFileWrite struct {
	Path    string `json:"path"`
	SavedAs string `json:"savedAs"`
}

// SharedFolder 是会话默认文件夹中共享文件区副本所在的子文件夹名称，与文件工具中指向共享文件区的路径前缀一致。
const SharedFolder = "shared"

// ComputerCall 是记录在工具调用上的电脑操作与电脑上报的结果。
type ComputerCall struct {
	Operation ComputerOperation `json:"operation"`
	Outcome   *ComputerOutcome  `json:"outcome,omitempty"`
}
