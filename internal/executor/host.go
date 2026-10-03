//go:build !server && !ios && !android

// Package executor 在这台电脑上执行服务端派发的操作：命令、文件读写、本机 MCP 工具调用与技能读取，
// 并以电脑身份连接服务端领取操作、上报结果与执行能力。
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/localmcp"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
	"github.com/runforyou-ai/luway/internal/integration/localworkspace"
)

// maxSkillFiles 是读取技能时列出的附带文件数量上限，多列出的一个表示还有更多文件。
const maxSkillFiles = 101

// HostOptions 定义执行操作使用的本机资源。
type HostOptions struct {
	// FolderRoot 是各会话默认文件夹的上级目录。
	FolderRoot string
	// Toolchain 返回命令与本机 MCP 服务使用的运行环境，以及托管的 uv、Node.js 与 Python 是否可用。
	Toolchain func() (localworkspace.Environment, bool)
	MCP       *localmcp.Store
	Skills    *localskill.Store
}

// Host 在这台电脑上执行操作并汇总执行能力，同一台电脑注册到的全部工作区共用。
type Host struct {
	options HostOptions
	mcp     *mcpPool

	mu        sync.Mutex
	observers map[int]func()
	next      int
}

// NewHost 创建本机执行环境，本机 MCP 服务在首次读取能力时连接。
func NewHost(options HostOptions) *Host {
	host := &Host{options: options, observers: map[int]func(){}}
	host.mcp = newMCPPool(options.FolderRoot, func() localworkspace.Environment {
		environment, _ := options.Toolchain()
		return environment
	})
	return host
}

// Subscribe 登记执行能力变化的观察者并返回取消登记的函数；观察者必须尽快返回。
func (h *Host) Subscribe(observer func()) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.next
	h.next++
	h.observers[id] = observer
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.observers, id)
	}
}

// Refresh 在本机 MCP 配置、技能或运行环境变化后重新连接本机 MCP 服务并读取工具目录，完成后通知观察者重新上报能力。
func (h *Host) Refresh() {
	go func() {
		h.syncMCP()
		h.mu.Lock()
		observers := slices.Collect(maps.Values(h.observers))
		h.mu.Unlock()
		for _, observer := range observers {
			observer()
		}
	}()
}

// syncMCP 按当前配置连接本机 MCP 服务，配置无法读取时保留现有连接。
func (h *Host) syncMCP() {
	servers, err := h.options.MCP.List()
	if err != nil {
		slog.Warn("读取本地 MCP 配置失败", "error", err)
		return
	}
	h.mcp.sync(servers)
}

// Capabilities 返回这台电脑当前的执行能力；本机 MCP 服务尚未连接时先连接并读取工具目录。
func (h *Host) Capabilities(ctx context.Context) (domain.ComputerCapabilities, error) {
	if !h.mcp.synced() {
		h.syncMCP()
	}
	_, managed := h.options.Toolchain()
	capabilities := domain.ComputerCapabilities{
		Shell: "bash", ManagedToolchain: managed, FolderRoot: h.options.FolderRoot, MCPServers: h.mcp.catalog(),
	}
	if runtime.GOOS == "windows" {
		capabilities.Shell = "PowerShell"
	}
	skills, err := h.options.Skills.List(ctx)
	if err != nil {
		return domain.ComputerCapabilities{}, fmt.Errorf("list local skills: %w", err)
	}
	capabilities.Skills = make([]domain.ComputerSkill, 0, len(skills))
	for _, skill := range skills {
		capabilities.Skills = append(capabilities.Skills, domain.ComputerSkill{Name: skill.Name, Description: skill.Description, Dir: skill.Dir, Fork: skill.Fork})
	}
	return capabilities, nil
}

// Execute 执行一次操作，失败原因写入结果的 Error 交给模型。
func (h *Host) Execute(ctx context.Context, operation domain.ComputerOperation) domain.ComputerOutcome {
	outcome, err := h.execute(ctx, operation)
	if err != nil {
		return domain.ComputerOutcome{Error: err.Error()}
	}
	return outcome
}

// execute 按操作原语在会话默认文件夹中执行操作。
func (h *Host) execute(ctx context.Context, operation domain.ComputerOperation) (domain.ComputerOutcome, error) {
	environment, _ := h.options.Toolchain()
	workspace := localworkspace.New(h.folder(operation.Folder), environment)
	switch operation.Kind {
	case domain.ComputerOperationReadFile:
		read, err := workspace.ReadText(operation.Path, operation.Offset, operation.Limit)
		if err != nil {
			return domain.ComputerOutcome{}, err
		}
		return domain.ComputerOutcome{Output: read.Content, Path: read.Path, Hash: read.Hash}, nil
	case domain.ComputerOperationWriteFile:
		version, err := workspace.WriteText(ctx, operation.Path, operation.Content, operation.BaseHash)
		if err != nil {
			return domain.ComputerOutcome{}, err
		}
		return domain.ComputerOutcome{Output: "已写入：" + version.Path, Path: version.Path, Hash: version.Hash}, nil
	case domain.ComputerOperationEditFile:
		version, err := workspace.EditText(ctx, operation.Path, operation.OldString, operation.NewString, operation.ReplaceAll, operation.BaseHash)
		if err != nil {
			return domain.ComputerOutcome{}, err
		}
		return domain.ComputerOutcome{Output: "已修改：" + version.Path, Path: version.Path, Hash: version.Hash}, nil
	case domain.ComputerOperationCommand:
		output, err := workspace.Run(ctx, operation.Command)
		if err != nil {
			return domain.ComputerOutcome{}, err
		}
		return domain.ComputerOutcome{Output: output}, nil
	case domain.ComputerOperationMCPCall:
		arguments := json.RawMessage(operation.Arguments)
		if !json.Valid(arguments) {
			return domain.ComputerOutcome{}, errors.New("参数不是合法 JSON，请重新提交。")
		}
		output, err := h.mcp.call(ctx, operation.MCPServer, operation.MCPTool, arguments)
		if err != nil {
			return domain.ComputerOutcome{}, err
		}
		return domain.ComputerOutcome{Output: output}, nil
	case domain.ComputerOperationLoadSkill:
		return h.loadSkill(ctx, operation.Skill)
	}
	return domain.ComputerOutcome{}, fmt.Errorf("这台电脑的应用不支持操作 %q，请更新应用", operation.Kind)
}

// folder 返回会话默认文件夹的绝对路径，名称不是单级文件夹时使用会话文件夹根目录。
func (h *Host) folder(name string) string {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return h.options.FolderRoot
	}
	return filepath.Join(h.options.FolderRoot, name)
}

// loadSkill 读取技能的说明正文与附带文件，附带文件跳过隐藏文件、依赖与缓存目录以及技能根目录的说明文件。
func (h *Host) loadSkill(ctx context.Context, name string) (domain.ComputerOutcome, error) {
	skill, body, err := h.options.Skills.Load(ctx, name)
	if err != nil {
		return domain.ComputerOutcome{}, fmt.Errorf("没有名为 %s 的技能", name)
	}
	files := make([]string, 0)
	_ = filepath.WalkDir(skill.Dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == skill.Dir {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "__pycache__" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() == localskill.FileName && filepath.Dir(path) == skill.Dir {
			return nil
		}
		if len(files) == maxSkillFiles {
			return filepath.SkipAll
		}
		relative, _ := filepath.Rel(skill.Dir, path)
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	return domain.ComputerOutcome{Output: body, Path: skill.Dir, Files: files}, nil
}

// Close 关闭全部本机 MCP 服务连接。
func (h *Host) Close() {
	h.mcp.close()
}
