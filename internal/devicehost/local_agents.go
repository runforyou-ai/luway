//go:build !server && !ios && !android

package devicehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/localworkspace"
	"github.com/runforyou-ai/luway/pkg/outputbuffer"
)

const (
	// codexACPPackage 是驱动 Codex 的 ACP 适配器 npm 包，版本随桌面端固定。
	codexACPPackage = "@agentclientprotocol/codex-acp"
	codexACPVersion = "1.13.1"
	// localAgentDetectInterval 是重新探测本机 Agent 的间隔。
	localAgentDetectInterval = 5 * time.Minute
	// localAgentProbeTimeout 是单次检查本机 Agent 登录状态的时限。
	localAgentProbeTimeout = 20 * time.Second
	// localAgentInstallTimeout 是安装 ACP 适配器的时限。
	localAgentInstallTimeout = 10 * time.Minute
	// localAgentStderrBytes 是本机 Agent 异常退出时记入日志的错误输出字节数。
	localAgentStderrBytes = 4 << 10
)

// errLocalAgentUnavailable 表示这台电脑上没有可用的指定本机 Agent。
var errLocalAgentUnavailable = errors.New("local agent unavailable")

// localAgents 探测这台电脑上可用的本机 Agent，准备其 ACP 适配器并在运行时启动。
type localAgents struct {
	toolchain Toolchain
	// dir 是 ACP 适配器的安装目录，位于运行环境目录下。
	dir string

	mu        sync.Mutex
	codexPath string // 最近一次探测到的已登录 Codex CLI 路径。
}

// detect 返回已安装、已登录且适配器就绪的本机 Agent；Codex 可用而适配器尚未安装时先安装。
func (l *localAgents) detect(ctx context.Context) []domain.LocalAgentKind {
	environment := l.toolchain.Environment()
	codexPath := ""
	defer func() {
		l.mu.Lock()
		l.codexPath = codexPath
		l.mu.Unlock()
	}()
	// 运行环境未就绪或用户已卸载时没有 Node.js，无法运行适配器。
	if len(environment.PathPrefix) == 0 {
		return []domain.LocalAgentKind{}
	}
	// 由 Codex 自身检查登录状态，应用不读取其凭据。
	probeCtx, cancel := context.WithTimeout(ctx, localAgentProbeTimeout)
	defer cancel()
	probe, err := localworkspace.Command(probeCtx, environment, "", "codex", "login", "status")
	if err != nil || probe.Run() != nil {
		return []domain.LocalAgentKind{}
	}
	if err := l.installCodexAdapter(ctx, environment); err != nil {
		slog.Warn("安装 Codex ACP 适配器失败", "version", codexACPVersion, "error", err)
		return []domain.LocalAgentKind{}
	}
	codexPath = probe.Path
	return []domain.LocalAgentKind{domain.LocalAgentKindCodex}
}

// codexAdapterDir 返回当前版本 Codex ACP 适配器的安装目录。
func (l *localAgents) codexAdapterDir() string {
	return filepath.Join(l.dir, "codex-acp", codexACPVersion)
}

// codexAdapterEntry 返回当前版本 Codex ACP 适配器的入口脚本。
func (l *localAgents) codexAdapterEntry() string {
	return filepath.Join(l.codexAdapterDir(), "node_modules", filepath.FromSlash(codexACPPackage), "dist", "index.js")
}

// installCodexAdapter 用运行环境中的 npm 把固定版本的适配器安装到临时目录，完成后整体换入安装目录；已安装时直接返回。
func (l *localAgents) installCodexAdapter(ctx context.Context, environment localworkspace.Environment) error {
	if _, err := os.Stat(l.codexAdapterEntry()); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.codexAdapterDir()), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(l.codexAdapterDir()), ".install-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	installCtx, cancel := context.WithTimeout(ctx, localAgentInstallTimeout)
	defer cancel()
	// 适配器使用负责人已安装的 Codex CLI，不安装其可选的自带 Codex。
	install, err := localworkspace.Command(installCtx, environment, staging, "npm", "install", "--prefix", staging,
		"--omit=optional", "--no-audit", "--no-fund", "--loglevel=error", codexACPPackage+"@"+codexACPVersion)
	if err != nil {
		return err
	}
	stderr := outputbuffer.New(0, localAgentStderrBytes)
	install.Stderr = stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("npm install: %w: %s", err, stderr.String())
	}
	// 并发安装已先完成时沿用已安装的版本。
	if err := os.Rename(staging, l.codexAdapterDir()); err != nil {
		if _, statErr := os.Stat(l.codexAdapterEntry()); statErr != nil {
			return err
		}
	}
	slog.Info("Codex ACP 适配器已安装", "version", codexACPVersion)
	return nil
}

// start 返回在会话默认文件夹中启动指定本机 Agent 的函数；Codex 以完全访问模式运行已登录的 Codex CLI，不提供浏览器登录。
func (l *localAgents) start(kind domain.LocalAgentKind, folder string) func(context.Context) (agentruntime.LocalAgentProcess, error) {
	return func(ctx context.Context) (agentruntime.LocalAgentProcess, error) {
		l.mu.Lock()
		codexPath := l.codexPath
		l.mu.Unlock()
		if kind != domain.LocalAgentKindCodex || codexPath == "" {
			return agentruntime.LocalAgentProcess{}, errLocalAgentUnavailable
		}
		if _, err := os.Stat(l.codexAdapterEntry()); err != nil {
			return agentruntime.LocalAgentProcess{}, errLocalAgentUnavailable
		}
		environment := l.toolchain.Environment()
		environment.Variables = append(slices.Clone(environment.Variables),
			"CODEX_PATH="+codexPath, "NO_BROWSER=1", "INITIAL_AGENT_MODE=agent-full-access")
		stderr := outputbuffer.New(0, localAgentStderrBytes)
		process, err := localworkspace.StartProcess(ctx, environment, folder, stderr, "node", l.codexAdapterEntry())
		if err != nil {
			return agentruntime.LocalAgentProcess{}, err
		}
		return agentruntime.LocalAgentProcess{Stdin: process.Stdin, Stdout: process.Stdout, Close: func() error {
			err := process.Close()
			if output := stderr.String(); output != "" {
				slog.Debug("本机 Agent 错误输出", "kind", kind, "stderr", output)
			}
			return err
		}}, nil
	}
}
