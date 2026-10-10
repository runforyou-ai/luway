// Package localagent 读取这台电脑上可以接受委派的本机 Agent：配置文件中添加的 Agent，与按已安装的命令行工具识别的常见 Agent。
//
// 配置文件使用 agents 格式：Agent 名称映射到用途说明、经 ACP 协议通信的启动命令、参数与环境变量。
package localagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

// namePattern 是 Agent 名称的格式：字母、数字、下划线与连字符，以字母或数字开头。
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Agent 是一个经 ACP 协议接入的本机 Agent：用途说明与经标准输入输出通信的启动命令、参数与环境变量。
type Agent struct {
	Name        string            `json:"-"`
	Description string            `json:"description,omitempty"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// Validate 校验 Agent 名称与启动命令。
func (a Agent) Validate() error {
	if !namePattern.MatchString(a.Name) {
		return errors.New("Agent 名称只能包含字母、数字、下划线与连字符，且以字母或数字开头")
	}
	if strings.TrimSpace(a.Command) == "" {
		return errors.New("Agent 需要启动命令")
	}
	return nil
}

// file 是配置文件的内容。
type file struct {
	Agents map[string]Agent `json:"agents"`
}

// Store 读取本机 Agent 配置文件。
type Store struct {
	path string
}

// NewStore 创建读取指定配置文件的存储。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// List 按名称顺序返回配置文件中格式有效的 Agent，配置文件不存在时返回空列表。
func (s *Store) List() ([]Agent, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Agent{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local agent config: %w", err)
	}
	var content file
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("decode local agent config: %w", err)
	}
	agents := make([]Agent, 0, len(content.Agents))
	for _, name := range slices.Sorted(maps.Keys(content.Agents)) {
		agent := content.Agents[name]
		agent.Name = name
		if err := agent.Validate(); err != nil {
			slog.WarnContext(context.Background(), "本机 Agent 配置无效，已跳过", "local_agent", name, "error", err)
			continue
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

// builtin 是按已安装的命令行工具识别的常见 Agent：requires 是识别所需的命令，Agent 经 npx 启动 ACP 适配器。
type builtin struct {
	Agent
	requires string
}

// builtins 是可以识别的常见 Agent。
var builtins = []builtin{
	{Agent: Agent{Name: "codex", Description: "Codex 编程 Agent，擅长编写、修改、调试代码与执行开发任务", Command: "npx", Args: []string{"-y", "@agentclientprotocol/codex-acp"}}, requires: "codex"},
	{Agent: Agent{Name: "claude", Description: "Claude Code 编程 Agent，擅长编写、修改、调试代码与执行开发任务", Command: "npx", Args: []string{"-y", "@agentclientprotocol/claude-agent-acp"}}, requires: "claude"},
}

// Merge 返回可用的本机 Agent，按名称排序：配置文件中的 Agent，加上 installed 判断已安装且与配置不重名的常见 Agent；常见 Agent 需要识别命令与 npx 都已安装。
func Merge(configured []Agent, installed func(command string) bool) []Agent {
	agents := slices.Clone(configured)
	for _, item := range builtins {
		taken := slices.ContainsFunc(agents, func(agent Agent) bool { return agent.Name == item.Name })
		if !taken && installed(item.requires) && installed(item.Command) {
			agents = append(agents, item.Agent)
		}
	}
	slices.SortFunc(agents, func(a, b Agent) int { return strings.Compare(a.Name, b.Name) })
	return agents
}
