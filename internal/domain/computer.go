package domain

import (
	"encoding/json"
	"time"
)

const (
	// ComputerPresenceTimeout 是电脑事件流两次在线记录的最长间隔，超过即视为离线。
	ComputerPresenceTimeout = time.Minute
	// ComputerOperationTimeout 是电脑执行单次操作的时限，命令超出即终止。
	ComputerOperationTimeout = 10 * time.Minute
)

// ComputerKind 定义电脑的归属类型。
type ComputerKind string

const (
	// ComputerKindPersonal 表示成员的个人电脑，只为该成员的个人 AI 员工执行操作。
	ComputerKindPersonal ComputerKind = "personal"
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
)

// ComputerOperation 是派发给电脑的一次操作；相对路径与命令工作目录以会话默认文件夹为起点。
type ComputerOperation struct {
	Kind ComputerOperationKind `json:"kind"`
	// Folder 是会话默认文件夹在执行器会话文件夹根目录下的名称。
	Folder string `json:"folder"`
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
	// Arguments 是 MCP 工具参数的 JSON 对象。
	Arguments string `json:"arguments,omitempty"`
	Skill     string `json:"skill,omitempty"`
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
}

// ComputerCall 是记录在工具调用上的电脑操作与电脑上报的结果。
type ComputerCall struct {
	Operation ComputerOperation `json:"operation"`
	Outcome   *ComputerOutcome  `json:"outcome,omitempty"`
}
