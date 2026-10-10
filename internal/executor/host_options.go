//go:build !server && !ios && !android

package executor

import (
	"path/filepath"

	"github.com/runforyou-ai/luway/internal/executor/localagent"
	"github.com/runforyou-ai/luway/internal/executor/localmcp"
	"github.com/runforyou-ai/luway/internal/executor/localskill"
	"github.com/runforyou-ai/luway/internal/executor/localworkspace"
)

const (
	// mcpConfigName 是数据目录中本机 MCP 配置文件的名称。
	mcpConfigName = "mcp.json"
	// agentConfigName 是数据目录中本机 Agent 配置文件的名称。
	agentConfigName = "agents.json"
)

// LocalConfig 定义本机执行环境的数据位置与平台能力，桌面端与无界面执行器各自给出。
type LocalConfig struct {
	// FolderRoot 是各会话默认文件夹的上级目录。
	FolderRoot string
	// DataDir 是本机 MCP 配置与本机 Agent 配置文件所在的目录。
	DataDir string
	// SkillDirs 是读取技能的目录，安装的技能写入第一个目录。
	SkillDirs []localskill.Dir
	// Toolchain 返回命令与本机 MCP 服务使用的运行环境，以及托管的 uv、Node.js 与 Python 是否可用。
	Toolchain func() (localworkspace.Environment, bool)
	// Browser 执行浏览器动作，为空表示这台电脑不支持浏览器操作。
	Browser BrowserDriver
	// Desktop 执行桌面动作，为空表示这台电脑不支持桌面操作。
	Desktop DesktopDriver
	// OnChange 在本机 MCP 配置或技能变化后调用。
	OnChange func()
}

// NewHostOptions 在数据目录中打开本机 MCP 与本机 Agent 配置、按技能目录打开技能存储，并带上平台给出的运行环境与驱动。
func NewHostOptions(config LocalConfig) HostOptions {
	return HostOptions{
		FolderRoot:  config.FolderRoot,
		Toolchain:   config.Toolchain,
		MCP:         localmcp.NewStore(filepath.Join(config.DataDir, mcpConfigName), config.OnChange),
		Skills:      localskill.NewStore(config.SkillDirs, config.OnChange),
		Browser:     config.Browser,
		Desktop:     config.Desktop,
		LocalAgents: localagent.NewStore(filepath.Join(config.DataDir, agentConfigName)),
	}
}
