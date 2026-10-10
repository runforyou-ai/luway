// Package toolchain 在本机准备 Agent 命令与本地 MCP 服务使用的 uv、Node.js 与默认 Python，并给出命令的环境变量。
//
// 工具链根目录下 dist/<名称>/<版本> 只放发行物，命令使用已安装的最高版本；python、uv-tools、npm-global 与 bin 跨版本保留。
// 桌面端启动后自动安装内置版本，之后由用户在设置中更新到下载源的最新版本；用户卸载后停止自动安装，直到重新安装。
// 下载源按本机公网出口所在地区选择：中国大陆使用国内镜像，其他地区使用官方源。
// 运行环境只作用于 Agent 执行的命令，不修改 shell 配置、系统 PATH 与 Windows 注册表。
// 命令优先使用托管解释器，项目已有的 .venv 与 .python-version 按 uv 的规则使用；用户 uv 配置中的离线与禁止下载设置不作用于 Agent 命令。
// 旧版本发行物由持有共享锁的进程保留，没有进程使用时才清理。
package toolchain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/gofrs/flock"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/executor/localworkspace"
	"github.com/runforyou-ai/support/arr"
	"golang.org/x/mod/semver"
)

const (
	// pythonInstallTimeout 是安装或升级默认 Python 的时限，超时按下载失败处理。
	pythonInstallTimeout = 30 * time.Minute
	// retryBaseInterval 是首次准备失败后的重试间隔，连续失败时逐次翻倍并随机浮动。
	retryBaseInterval = 30 * time.Second
	// retryMaxInterval 是准备失败后重试间隔翻倍的上限。
	retryMaxInterval = 30 * time.Minute
	// staleStagingAge 是清理中断遗留解压目录的最短存在时长。
	staleStagingAge = 24 * time.Hour
	// defaultPythonMarker 是记录已安装默认 Python 版本的文件名。
	defaultPythonMarker = "default-python"
)

// ErrBusy 表示运行环境正在准备或更新。
var ErrBusy = errors.New("toolchain is being prepared or updated")

// ErrNotReady 表示运行环境尚未完成首次准备。
var ErrNotReady = errors.New("toolchain is not ready")

// State 是运行环境的准备状态。
type State string

const (
	// StatePreparing 表示运行环境正在准备或尚未开始准备。
	StatePreparing State = "preparing"
	// StateReady 表示已有可用的运行环境。
	StateReady State = "ready"
	// StateFailed 表示最近一次准备失败，等待自动重试。
	StateFailed State = "failed"
	// StateUninstalled 表示用户已卸载运行环境。
	StateUninstalled State = "uninstalled"
)

// Failure 是运行环境准备失败的原因。
type Failure string

const (
	// FailureDownload 表示无法从下载源取得安装文件。
	FailureDownload Failure = "download"
	// FailureVerify 表示下载的安装文件校验失败。
	FailureVerify Failure = "verify"
	// FailureInstall 表示在本机安装失败。
	FailureInstall Failure = "install"
)

// Status 是运行环境的准备状态，失败原因只在失败状态下非空；Updating 表示正在按用户请求更新。
type Status struct {
	State    State
	Failure  Failure
	Updating bool
}

// Info 是运行环境的安装位置与各组件当前使用的版本，未安装的组件版本为空。
type Info struct {
	Root   string
	UV     string
	Node   string
	Python string
}

// stepError 是标明失败原因的准备错误。
type stepError struct {
	failure Failure
	err     error
}

// Error 返回底层错误的说明。
func (e *stepError) Error() string { return e.err.Error() }

// Unwrap 返回底层错误。
func (e *stepError) Unwrap() error { return e.err }

// FailureOf 返回准备或更新错误的失败原因，未标明原因的错误发生在本机安装步骤。
func FailureOf(err error) Failure {
	if step, ok := errors.AsType[*stepError](err); ok {
		return step.failure
	}
	return FailureInstall
}

// Sources 是下载源，空字段使用官方源。
type Sources struct {
	NodeDownloadURL     string
	PythonInstallMirror string
	PyPIIndexURL        string
	NPMRegistry         string
}

// Manager 在后台准备本机运行环境，按用户请求更新，并给出 Agent 命令使用的环境变量。
type Manager struct {
	root     string
	cache    string
	client   *http.Client
	onChange func()

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu           sync.Mutex
	preparing    bool
	updating     bool
	uninstalling bool
	// stopPrepare 取消进行中的准备，prepareDone 在准备结束时关闭。
	stopPrepare context.CancelFunc
	prepareDone chan struct{}
	// retry 计算连续准备失败后的重试间隔，准备成功、卸载或重新安装时重置。
	retry   *backoff.ExponentialBackOff
	retryAt time.Time
	// failure 是最近一次准备失败的原因，准备成功后清空。
	failure Failure
	// inUse 按「名称/版本」保存本进程命令使用过的发行物共享锁，进程结束前不释放。
	inUse map[string]*flock.Flock

	sourcesMu sync.Mutex
	// sources 是探测成功后按地区选定的下载源，探测成功前为空。
	sources *Sources
}

// DefaultDirs 返回当前操作系统用户的工具链根目录与缓存目录，不随 XDG 变量变化。
func DefaultDirs() (string, string, error) {
	if runtime.GOOS == "windows" {
		// Windows 的用户缓存目录即 %LOCALAPPDATA%。
		local, err := os.UserCacheDir()
		if err != nil {
			return "", "", err
		}
		return filepath.Join(local, brand.Build().Slug, "toolchains"), filepath.Join(local, brand.Build().Slug, "cache"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	cache := filepath.Join(home, ".cache", brand.Build().Slug)
	if runtime.GOOS == "darwin" {
		cache = filepath.Join(home, "Library", "Caches", brand.Build().Slug)
	}
	return filepath.Join(home, ".local", "share", brand.Build().Slug, "toolchains"), cache, nil
}

// New 创建运行环境管理器并清理没有进程使用的旧版本发行物；onChange 在准备或更新开始与结束时调用。
func New(root, cache string, onChange func()) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{root: root, cache: cache, client: &http.Client{}, onChange: onChange, ctx: ctx, cancel: cancel, inUse: map[string]*flock.Flock{}}
	m.retry = &backoff.ExponentialBackOff{InitialInterval: retryBaseInterval, RandomizationFactor: 0.2, Multiplier: 2, MaxInterval: retryMaxInterval}
	m.retry.Reset()
	m.removeStale()
	return m
}

// Close 取消进行中的准备，等待其退出后释放本进程持有的发行物共享锁。
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, lock := range m.inUse {
		_ = lock.Unlock()
	}
	clear(m.inUse)
}

// Ensure 在已安装版本低于内置版本或默认 Python 低于内置版本时于后台安装内置版本，失败后按退避间隔重试；用户已卸载或正在卸载时不安装。
// 返回运行环境是否已确定：已有可用的运行环境，或用户已卸载运行环境。
func (m *Manager) Ensure() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.uninstalling {
		return false
	}
	if m.uninstalled() {
		return true
	}
	if !m.preparing && !m.updating && !m.upToDate() && !time.Now().Before(m.retryAt) && m.ctx.Err() == nil {
		ctx, cancel := context.WithCancel(m.ctx)
		m.preparing, m.stopPrepare, m.prepareDone = true, cancel, make(chan struct{})
		m.wg.Add(1)
		go m.prepare(ctx)
	}
	return m.usable()
}

// Uninstall 先取消并等待进行中的准备，再删除运行环境的全部文件与下载缓存，全部删除后记录用户已卸载；正在更新或卸载时返回 ErrBusy。
// 删除失败时不记录卸载，运行环境保持未卸载状态，用户可以关闭占用文件的程序后重试。
func (m *Manager) Uninstall() error {
	m.mu.Lock()
	if m.updating || m.uninstalling {
		m.mu.Unlock()
		return ErrBusy
	}
	m.uninstalling = true
	stop, done, preparing := m.stopPrepare, m.prepareDone, m.preparing
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.uninstalling = false
		m.mu.Unlock()
		m.onChange()
	}()
	if preparing {
		stop()
		<-done
	}
	m.mu.Lock()
	// 释放本进程的共享锁，锁文件随目录一起删除。
	for _, lock := range m.inUse {
		_ = lock.Unlock()
	}
	clear(m.inUse)
	m.retry.Reset()
	m.retryAt, m.failure = time.Time{}, ""
	m.mu.Unlock()
	if err := errors.Join(os.RemoveAll(m.root), os.RemoveAll(m.cache)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.root), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(m.uninstalledMarker(), nil, 0o644); err != nil {
		return err
	}
	slog.InfoContext(context.Background(), "Agent 运行环境已卸载", "root", m.root)
	return nil
}

// Install 清除卸载记录并在后台重新安装内置版本。
func (m *Manager) Install() error {
	if err := os.Remove(m.uninstalledMarker()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	m.mu.Lock()
	m.retry.Reset()
	m.retryAt, m.failure = time.Time{}, ""
	m.mu.Unlock()
	m.Ensure()
	m.onChange()
	return nil
}

// uninstalledMarker 返回记录用户已卸载的文件，位于工具链根目录旁。
func (m *Manager) uninstalledMarker() string {
	return m.root + ".uninstalled"
}

// uninstalled 判断用户是否已卸载运行环境。
func (m *Manager) uninstalled() bool {
	_, err := os.Stat(m.uninstalledMarker())
	return err == nil
}

// downloadSources 返回按地区选定的下载源：尚无探测成功的结果时探测本机公网出口所在地区，只缓存探测成功的结果，失败时本次使用国内镜像。
func (m *Manager) downloadSources(ctx context.Context) Sources {
	m.sourcesMu.Lock()
	defer m.sourcesMu.Unlock()
	if m.sources != nil {
		return *m.sources
	}
	sources, detected := detectSources(ctx, m.client, regionTraceURL)
	if detected {
		m.sources = &sources
	}
	return sources
}

// Status 返回运行环境的准备状态：已有可用环境时为就绪；已有旧版本时后台安装新内置版本的进度与失败不在界面展示，命令继续使用旧版本。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.uninstalled():
		return Status{State: StateUninstalled}
	case m.usable():
		return Status{State: StateReady, Updating: m.updating}
	case m.failure != "" && !m.preparing:
		return Status{State: StateFailed, Failure: m.failure}
	default:
		return Status{State: StatePreparing}
	}
}

// Info 返回安装位置与各组件当前使用的版本。
func (m *Manager) Info() Info {
	python, _ := os.ReadFile(filepath.Join(m.root, defaultPythonMarker))
	return Info{Root: m.root, UV: m.activeVersion("uv"), Node: m.activeVersion("node"), Python: strings.TrimSpace(string(python))}
}

// Environment 返回 Agent 命令的环境变量：前置当前版本的 uv、Node.js 与稳定命令目录，安装与缓存限定在工具链目录；用户已卸载时不做改动。
// 命令使用的发行物在本进程结束前保持共享锁，其他进程不会将其清理。
func (m *Manager) Environment() localworkspace.Environment {
	if m.uninstalled() {
		return localworkspace.Environment{}
	}
	return m.environment(m.downloadSources(m.ctx), m.lockActive("uv"), m.lockActive("node"))
}

// lockActive 按版本从高到低选出取得共享锁且目录仍在的版本，正被其他进程清理的版本跳过；没有可用版本时返回空串。
func (m *Manager) lockActive(name string) string {
	for _, version := range m.versions(name) {
		if m.use(name, version) && dirExists(filepath.Join(m.root, "dist", name, version)) {
			return version
		}
	}
	return ""
}

// use 为本进程使用的发行物版本取得共享锁，已持有时直接返回 true；其他进程正在清理该版本时返回 false。
func (m *Manager) use(name, version string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := name + "/" + version
	if m.inUse[key] != nil {
		return true
	}
	lock := flock.New(filepath.Join(m.root, "dist", name, version+".lock"))
	if locked, err := lock.TryRLock(); err != nil || !locked {
		return false
	}
	m.inUse[key] = lock
	return true
}

// environment 返回使用指定 uv 与 Node.js 版本的命令环境变量，镜像只在环境变量中注入。
func (m *Manager) environment(sources Sources, uv, node string) localworkspace.Environment {
	bin := filepath.Join(m.root, "bin")
	nodeBin := filepath.Join(m.root, "dist", "node", node)
	npmBin := filepath.Join(m.root, "npm-global")
	// Unix 的 Node.js 与 npm 全局命令位于 bin 子目录，Windows 位于目录本身。
	if runtime.GOOS != "windows" {
		nodeBin = filepath.Join(nodeBin, "bin")
		npmBin = filepath.Join(npmBin, "bin")
	}
	variables := []string{
		// 优先使用托管解释器，项目中基于系统解释器的 .venv 保持可用。
		"UV_PYTHON_PREFERENCE=managed",
		// 覆盖用户 uv 配置与登录环境中的离线和禁止下载设置。
		"UV_PYTHON_DOWNLOADS=automatic",
		"UV_OFFLINE=false",
		// pip 只在虚拟环境中安装。
		"PIP_REQUIRE_VIRTUALENV=true",
		"UV_PYTHON_INSTALL_DIR=" + filepath.Join(m.root, "python"),
		"UV_PYTHON_BIN_DIR=" + bin,
		"UV_PYTHON_INSTALL_REGISTRY=0",
		"UV_TOOL_DIR=" + filepath.Join(m.root, "uv-tools"),
		"UV_TOOL_BIN_DIR=" + bin,
		"UV_CACHE_DIR=" + filepath.Join(m.cache, "uv"),
		"NPM_CONFIG_PREFIX=" + filepath.Join(m.root, "npm-global"),
		"NPM_CONFIG_CACHE=" + filepath.Join(m.cache, "npm"),
	}
	for _, mirror := range []struct{ name, value string }{
		{"UV_PYTHON_INSTALL_MIRROR", sources.PythonInstallMirror},
		{"UV_DEFAULT_INDEX", sources.PyPIIndexURL},
		{"NPM_CONFIG_REGISTRY", sources.NPMRegistry},
	} {
		if mirror.value != "" {
			variables = append(variables, mirror.name+"="+mirror.value)
		}
	}
	return localworkspace.Environment{
		PathPrefix: []string{filepath.Join(m.root, "dist", "uv", uv), nodeBin, bin, npmBin},
		Variables:  variables,
	}
}

// prepare 安装内置版本的运行环境，结束后记录重试时间并通知调用方；被取消的准备不计为失败。
func (m *Manager) prepare(ctx context.Context) {
	defer m.wg.Done()
	m.onChange()
	err := m.installBaseline(ctx, m.downloadSources(ctx))
	cancelled := ctx.Err() != nil
	m.mu.Lock()
	m.preparing = false
	m.stopPrepare()
	close(m.prepareDone)
	switch {
	case err == nil:
		m.retry.Reset()
		m.retryAt, m.failure = time.Time{}, ""
	case cancelled:
	default:
		m.retryAt = time.Now().Add(m.retry.NextBackOff())
		m.failure = FailureOf(err)
	}
	retryAt := m.retryAt
	m.mu.Unlock()
	switch {
	case err == nil:
		slog.InfoContext(ctx, "Agent 运行环境已就绪", "info", m.Info())
		m.removeStale()
	case !cancelled:
		slog.WarnContext(ctx, "准备 Agent 运行环境失败", "error", err, "retry_at", retryAt)
	}
	m.onChange()
}

// installBaseline 下载并校验内置版本的 uv 与 Node.js（已安装不低于内置版本时跳过），再用 uv 安装内置版本的默认 Python。
func (m *Manager) installBaseline(ctx context.Context, sources Sources) error {
	uvItem, uvFound := uvArtifacts[platform()]
	nodeItem, nodeFound := nodeArtifacts[platform()]
	if !uvFound || !nodeFound {
		return fmt.Errorf("unsupported platform %s", platform())
	}
	if !atLeast(m.activeVersion("uv"), uvVersion) {
		// uv 取自 PyPI 上的 wheel，与 Python 包使用同一个索引。
		resolve := func(ctx context.Context) (string, error) {
			links, err := indexLinks(ctx, m.client, cmp.Or(sources.PyPIIndexURL, defaultPyPIIndexURL), "uv")
			if err != nil {
				return "", err
			}
			for _, link := range links {
				if link.file == uvItem.file {
					return link.url, nil
				}
			}
			return "", &stepError{failure: FailureDownload, err: fmt.Errorf("package index does not list %s", uvItem.file)}
		}
		if err := m.installDist(ctx, "uv", uvVersion, resolve, uvItem, uvWheelContent(uvVersion)); err != nil {
			return err
		}
	}
	if !atLeast(m.activeVersion("node"), nodeVersion) {
		resolve := func(context.Context) (string, error) {
			return nodeFileURL(sources, nodeVersion, nodeItem.file), nil
		}
		if err := m.installDist(ctx, "node", nodeVersion, resolve, nodeItem, ""); err != nil {
			return err
		}
	}
	if python, _ := os.ReadFile(filepath.Join(m.root, defaultPythonMarker)); atLeast(strings.TrimSpace(string(python)), PythonVersion) {
		return nil
	}
	return m.installPython(ctx, sources, PythonVersion, false)
}

// installPython 用 uv 安装指定的默认 Python，upgrade 为 true 时升级到该系列的最新补丁版本，完成后记录实际版本。
// 直接运行工具链中的 uv 与 python，不经过 shell，不读取用户的 uv 配置文件。
func (m *Manager) installPython(ctx context.Context, sources Sources, request string, upgrade bool) error {
	uv, node := m.lockActive("uv"), m.lockActive("node")
	if uv == "" || node == "" {
		return errors.New("uv or Node.js is not installed")
	}
	environment := m.environment(sources, uv, node)
	args := []string{"python", "install", request, "--default", "--no-registry", "--no-config", "--preview-features", "python-install-default"}
	if upgrade {
		args = append(args, "--upgrade")
	}
	installCtx, cancel := context.WithTimeout(ctx, pythonInstallTimeout)
	defer cancel()
	output, err := m.run(installCtx, environment, "uv", args...)
	if err != nil {
		err = fmt.Errorf("install python: %w: %s", err, output)
		// uv 下载解释器失败时输出 Failed to download，超时通常发生在下载过程中。
		if errors.Is(installCtx.Err(), context.DeadlineExceeded) || strings.Contains(output, "Failed to download") {
			return &stepError{failure: FailureDownload, err: err}
		}
		return err
	}
	// 以默认 python 命令报告的版本作为记录。
	version, err := m.run(ctx, environment, "python", "-c", "import platform; print(platform.python_version())")
	if err != nil {
		return fmt.Errorf("read python version: %w: %s", err, version)
	}
	return os.WriteFile(filepath.Join(m.root, defaultPythonMarker), []byte(version), 0o644)
}

// run 在运行环境中直接运行工具链命令，返回去掉首尾空白的合并输出。
func (m *Manager) run(ctx context.Context, environment localworkspace.Environment, name string, args ...string) (string, error) {
	cmd, err := localworkspace.Command(ctx, environment, m.root, name, args...)
	if err != nil {
		return "", err
	}
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// installDist 下载并解压一个发行物到 dist/<名称>/<版本>，目录已存在时直接返回；下载地址在需要下载时解析，content 是压缩包内作为版本目录的目录。
func (m *Manager) installDist(ctx context.Context, name, version string, resolveURL func(context.Context) (string, error), item artifact, content string) error {
	target := filepath.Join(m.root, "dist", name, version)
	if dirExists(target) {
		return nil
	}
	cacheDir := filepath.Join(m.cache, "downloads", name, version)
	archivePath := filepath.Join(cacheDir, item.file)
	// 缓存中已有校验一致的压缩包时直接使用，跳过解析下载地址。
	if checksum(archivePath) != item.sha256 {
		url, err := resolveURL(ctx)
		if err != nil {
			return err
		}
		if archivePath, err = download(ctx, m.client, url, cacheDir, item); err != nil {
			return err
		}
	}
	err := install(archivePath, target, content)
	// 解压成功后删除压缩包所在的缓存目录。
	if err == nil {
		_ = os.RemoveAll(filepath.Dir(archivePath))
	}
	// 其他进程已先完成同一版本时视为成功。
	if err != nil && dirExists(target) {
		return nil
	}
	return err
}

// upToDate 判断已安装的 uv、Node.js 与默认 Python 都不低于内置版本。
func (m *Manager) upToDate() bool {
	python, _ := os.ReadFile(filepath.Join(m.root, defaultPythonMarker))
	return atLeast(strings.TrimSpace(string(python)), PythonVersion) && atLeast(m.activeVersion("uv"), uvVersion) && atLeast(m.activeVersion("node"), nodeVersion)
}

// usable 判断是否已有可用的 uv、Node.js 与默认 Python，版本可以低于内置版本。
func (m *Manager) usable() bool {
	_, err := os.Stat(filepath.Join(m.root, defaultPythonMarker))
	return err == nil && m.activeVersion("uv") != "" && m.activeVersion("node") != ""
}

// activeVersion 返回命令优先使用的发行物版本，即已安装的最高版本，没有已安装版本时返回空串。
func (m *Manager) activeVersion(name string) string {
	if versions := m.versions(name); len(versions) > 0 {
		return versions[0]
	}
	return ""
}

// versions 按语义化版本从高到低返回已安装的发行物版本。
func (m *Manager) versions(name string) []string {
	entries, _ := os.ReadDir(filepath.Join(m.root, "dist", name))
	versions := arr.FilterMap(entries, func(entry os.DirEntry) (string, bool) {
		return entry.Name(), entry.IsDir() && semver.IsValid("v"+entry.Name())
	})
	slices.SortFunc(versions, func(a, b string) int { return semver.Compare("v"+b, "v"+a) })
	return versions
}

// removeStale 删除中断遗留的解压目录，以及低于当前最高版本且没有任何进程持有共享锁的发行物。
func (m *Manager) removeStale() {
	for _, name := range []string{"uv", "node"} {
		directory := filepath.Join(m.root, "dist", name)
		active := m.activeVersion(name)
		entries, _ := os.ReadDir(directory)
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			if strings.HasPrefix(entry.Name(), ".staging-") {
				if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > staleStagingAge {
					_ = os.RemoveAll(path)
				}
				continue
			}
			if !entry.IsDir() || entry.Name() == active {
				continue
			}
			// 取得独占锁说明没有进程在使用该版本。
			lock := flock.New(path + ".lock")
			if locked, err := lock.TryLock(); err != nil || !locked {
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				slog.WarnContext(context.Background(), "清理旧版本运行环境失败", "path", path, "error", err)
			}
			_ = lock.Unlock()
			_ = os.Remove(path + ".lock")
		}
	}
}

// atLeast 判断已安装版本不低于目标版本，未安装时返回 false。
func atLeast(installed, target string) bool {
	return installed != "" && semver.Compare("v"+installed, "v"+target) >= 0
}

// nodeFileURL 返回 Node.js 发行物在下载源中的地址。
func nodeFileURL(sources Sources, version, file string) string {
	return strings.Join([]string{cmp.Or(sources.NodeDownloadURL, defaultNodeDownloadURL), "v" + version, file}, "/")
}

// dirExists 判断路径是否为已存在的目录。
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
