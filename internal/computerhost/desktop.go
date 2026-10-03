//go:build !server && !ios && !android

package computerhost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/executor"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/localmcp"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
	"github.com/runforyou-ai/luway/internal/integration/localworkspace"
	"github.com/runforyou-ai/luway/internal/integration/toolchain"
	desktopmodels "github.com/runforyou-ai/luway/internal/storage/desktop/models"
)

const (
	// localMCPConfigName 是数据目录中本地 MCP 配置文件的名称。
	localMCPConfigName = "mcp.json"
	// syncTimeout 是读取本机注册结果的时限。
	syncTimeout = 30 * time.Second
	// checkInterval 是重新检查运行环境与注册结果的间隔，运行环境准备失败后据此按退避重试。
	checkInterval = 30 * time.Second
)

// DesktopOptions 定义桌面端电脑使用的数据目录、界面通知与系统文件管理器。
type DesktopOptions struct {
	DataDir    string
	Notify     func()
	OpenFolder func(string) error
}

// Desktop 组合桌面端电脑注册、执行器连接、运行环境、本地 MCP 服务配置与技能目录。
type Desktop struct {
	*Registrar
	host       *executor.Host
	toolchain  *toolchain.Manager
	localMCP   *localmcp.Store
	skills     *localskill.Store
	folderRoot string
	openFolder func(string) error

	mu    sync.Mutex
	links map[desktopmodels.ComputerRegistration]*executor.Link
	sync  chan struct{}
	ctx   context.Context
	stop  context.CancelFunc
	done  chan struct{}
}

// CurrentComputer 返回本机电脑注册状态与运行环境的准备状态。
func (d *Desktop) CurrentComputer(ctx context.Context, meta appservice.RequestMeta) (appservice.LocalComputer, error) {
	computer, err := d.Registrar.CurrentComputer(ctx, meta)
	if err != nil {
		return computer, err
	}
	local := d.toolchainStatus()
	computer.Toolchain = &local
	return computer, nil
}

// LocalEnvironment 返回运行环境的状态、安装位置、各组件版本、本地 MCP 服务与技能。
func (d *Desktop) LocalEnvironment(ctx context.Context, _ appservice.RequestMeta) (appservice.LocalEnvironment, error) {
	info := d.toolchain.Info()
	servers, err := d.localMCP.List()
	if err != nil {
		return appservice.LocalEnvironment{}, err
	}
	environment := appservice.LocalEnvironment{
		Toolchain: d.toolchainStatus(), Location: info.Root, UVVersion: info.UV, NodeVersion: info.Node, PythonVersion: info.Python,
		MCPServers: make([]appservice.LocalMCPServer, 0, len(servers)),
	}
	for _, server := range servers {
		environment.MCPServers = append(environment.MCPServers, appservice.LocalMCPServer{
			Name: server.Name, Type: appservice.LocalMCPServerType(server.Transport()), Command: server.Command, Args: server.Args, URL: server.URL,
		})
	}
	skills, err := d.skills.List(ctx)
	if err != nil {
		return appservice.LocalEnvironment{}, err
	}
	environment.Skills = make([]appservice.LocalSkill, len(skills))
	for i, skill := range skills {
		environment.Skills[i] = appservice.LocalSkill{
			Name: skill.Name, Description: skill.Description, Source: appservice.LocalSkillSource(skill.Source), Location: skill.Dir,
		}
	}
	return environment, nil
}

// UpdateLocalToolchain 把运行环境更新到下载源的最新版本，失败原因转换为本地化错误。
func (d *Desktop) UpdateLocalToolchain(ctx context.Context, meta appservice.RequestMeta) (appservice.LocalToolchainUpdate, error) {
	updated, err := d.toolchain.Update(ctx)
	switch {
	case err == nil:
		return appservice.LocalToolchainUpdate{Updated: updated}, nil
	case errors.Is(err, toolchain.ErrBusy):
		return appservice.LocalToolchainUpdate{}, appservice.ConflictError(meta, i18n.ErrorLocalToolchainBusy, "toolchain_busy")
	case errors.Is(err, toolchain.ErrNotReady):
		return appservice.LocalToolchainUpdate{}, appservice.ConflictError(meta, i18n.ErrorLocalToolchainNotReady, "toolchain_not_ready")
	}
	slog.Warn("更新运行环境失败", "error", err)
	key := map[toolchain.Failure]i18n.Key{
		toolchain.FailureDownload: i18n.ErrorLocalToolchainDownload,
		toolchain.FailureVerify:   i18n.ErrorLocalToolchainVerify,
		toolchain.FailureInstall:  i18n.ErrorLocalToolchainInstall,
	}[toolchain.FailureOf(err)]
	return appservice.LocalToolchainUpdate{}, appservice.FailedError(meta, key)
}

// UninstallLocalToolchain 删除运行环境的全部文件与下载缓存，重新安装前不再自动安装。
func (d *Desktop) UninstallLocalToolchain(_ context.Context, meta appservice.RequestMeta) error {
	err := d.toolchain.Uninstall()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, toolchain.ErrBusy):
		return appservice.ConflictError(meta, i18n.ErrorLocalToolchainBusy, "toolchain_busy")
	}
	slog.Warn("卸载运行环境失败", "error", err)
	return appservice.FailedError(meta, i18n.ErrorLocalToolchainUninstall)
}

// InstallLocalToolchain 清除卸载记录并在后台重新安装运行环境。
func (d *Desktop) InstallLocalToolchain(context.Context, appservice.RequestMeta) error {
	return d.toolchain.Install()
}

// OpenLocalToolchainFolder 在系统文件管理器中打开运行环境的安装位置。
func (d *Desktop) OpenLocalToolchainFolder(context.Context, appservice.RequestMeta) error {
	root := d.toolchain.Info().Root
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return d.openFolder(root)
}

// AddLocalMCPServer 在运行环境中试启动本地 MCP 服务并读取工具目录，成功后保存配置，同名服务被替换；试启动失败时返回原因与服务的错误输出。
func (d *Desktop) AddLocalMCPServer(ctx context.Context, meta appservice.RequestMeta, input appservice.LocalMCPServerInput) error {
	server := localmcp.Server{
		Name: strings.TrimSpace(input.Name), Type: string(input.Type), Command: strings.TrimSpace(input.Command), Args: input.Args,
		Env: input.Env, URL: strings.TrimSpace(input.URL), Headers: input.Headers,
	}
	if server.Transport() == localmcp.TypeStdio {
		server.Type = ""
	}
	environment, _ := d.environment()
	if err := os.MkdirAll(d.folderRoot, 0o755); err != nil {
		return err
	}
	// 试启动失败时在本地化说明后附上原因与服务的错误输出。
	if err := executor.ProbeMCPServer(ctx, server, environment, d.folderRoot); err != nil {
		failure := appservice.FailedError(meta, i18n.ErrorLocalMCPServerStartFailed)
		failure.Message += "\n" + err.Error()
		return failure
	}
	return d.localMCP.Put(server)
}

// RemoveLocalMCPServer 删除本地 MCP 服务配置。
func (d *Desktop) RemoveLocalMCPServer(_ context.Context, meta appservice.RequestMeta, name string) error {
	removed, err := d.localMCP.Remove(name)
	if err != nil {
		return err
	}
	if !removed {
		return appservice.NotFoundError(meta, i18n.ErrorLocalMCPServerNotFound)
	}
	return nil
}

// InstallLocalSkill 从来源安装技能，来源含多个技能时按名称选择，同名技能被替换；失败时返回原因。
func (d *Desktop) InstallLocalSkill(ctx context.Context, meta appservice.RequestMeta, input appservice.LocalSkillInstallInput) error {
	// 安装失败时在本地化说明后附上原因。
	if _, err := d.skills.Install(ctx, strings.TrimSpace(input.Source), strings.TrimSpace(input.Name)); err != nil {
		failure := appservice.FailedError(meta, i18n.ErrorLocalSkillInstallFailed)
		failure.Message += "\n" + err.Error()
		return failure
	}
	return nil
}

// RemoveLocalSkill 删除个人 AI 员工安装的技能。
func (d *Desktop) RemoveLocalSkill(ctx context.Context, meta appservice.RequestMeta, name string) error {
	removed, err := d.skills.Remove(ctx, name)
	if err != nil {
		return err
	}
	if !removed {
		return appservice.NotFoundError(meta, i18n.ErrorLocalSkillNotFound)
	}
	return nil
}

// toolchainStatus 返回运行环境的准备状态。
func (d *Desktop) toolchainStatus() appservice.LocalToolchain {
	status := d.toolchain.Status()
	local := appservice.LocalToolchain{State: appservice.LocalToolchainState(status.State), Updating: status.Updating}
	if status.Failure != "" {
		failure := appservice.LocalToolchainFailure(status.Failure)
		local.Failure = &failure
	}
	return local
}

// environment 返回命令与本机 MCP 服务使用的运行环境，以及托管的 uv、Node.js 与 Python 是否可用。
func (d *Desktop) environment() (localworkspace.Environment, bool) {
	environment := d.toolchain.Environment()
	return environment, len(environment.PathPrefix) > 0
}

// Start 开始准备运行环境、电脑注册与执行器连接。
func (d *Desktop) Start() {
	d.Registrar.Start()
	go d.run()
}

// Stop 结束执行器连接与电脑注册并等待其退出。
func (d *Desktop) Stop() {
	d.stop()
	<-d.done
	d.Registrar.Stop()
	d.toolchain.Close()
	d.host.Close()
}

// run 定期准备运行环境，并在注册结果变化时按本机全部注册结果增减执行器连接，直到停止。
func (d *Desktop) run() {
	defer close(d.done)
	defer func() {
		d.mu.Lock()
		links := d.links
		d.links = map[desktopmodels.ComputerRegistration]*executor.Link{}
		d.mu.Unlock()
		for _, link := range links {
			link.Stop()
		}
	}()
	for {
		d.toolchain.Ensure()
		d.syncLinks()
		select {
		case <-d.ctx.Done():
			return
		case <-d.sync:
		case <-time.After(checkInterval):
		}
	}
}

// syncLinks 为每个注册结果维持一条执行器连接，与登录会话无关；已删除的注册结果停止连接，凭据失效的连接删除对应注册结果。
func (d *Desktop) syncLinks() {
	ctx, cancel := context.WithTimeout(d.ctx, syncTimeout)
	registrations, err := d.Registrations(ctx)
	cancel()
	if err != nil {
		slog.Warn("读取电脑注册结果失败", "error", err)
		return
	}
	current := make(map[desktopmodels.ComputerRegistration]bool, len(registrations))
	for _, registration := range registrations {
		registration.RegisteredAt = ""
		current[registration] = true
		d.mu.Lock()
		_, running := d.links[registration]
		d.mu.Unlock()
		if running {
			continue
		}
		link, err := executor.StartLink(executor.LinkOptions{
			ServerURL: registration.ServerURL, Credential: registration.Credential, Host: d.host,
			OnCredentialInvalid: func() { d.Forget(context.WithoutCancel(d.ctx), registration) },
		})
		if err != nil {
			slog.Warn("建立执行器连接失败", "server_url", registration.ServerURL, "organization_id", registration.OrganizationID, "error", err)
			continue
		}
		d.mu.Lock()
		d.links[registration] = link
		d.mu.Unlock()
	}
	d.mu.Lock()
	stale := make([]*executor.Link, 0)
	for registration, link := range d.links {
		if !current[registration] {
			stale = append(stale, link)
			delete(d.links, registration)
		}
	}
	d.mu.Unlock()
	for _, link := range stale {
		link.Stop()
	}
}

// NewDesktop 创建桌面端电脑注册与执行器，各会话的默认文件夹位于用户文档目录下以产品名称命名的文件夹；当前平台不作为电脑或本机目录无法确定时返回 nil。
func NewDesktop(store Store, client Client, sessions *clientsession.Manager, options DesktopOptions) *Desktop {
	registrar := New(store, client, sessions)
	if registrar == nil {
		return nil
	}
	// 无法确定文档目录时默认文件夹放在系统临时目录下，执行照常进行。
	documents, err := DocumentsDir()
	if err != nil {
		slog.Warn("无法确定用户文档目录，会话默认文件夹改放在临时目录", "error", err)
		documents = os.TempDir()
	}
	toolchainRoot, toolchainCache, err := toolchain.DefaultDirs()
	if err != nil {
		slog.Error("无法确定运行环境目录，这台电脑不注册", "error", err)
		return nil
	}
	skillDirs, err := localskill.DefaultDirs()
	if err != nil {
		slog.Error("无法确定技能目录，这台电脑不注册", "error", err)
		return nil
	}
	ctx, stop := context.WithCancel(context.Background())
	desktop := &Desktop{
		Registrar: registrar, folderRoot: filepath.Join(documents, brand.Build().DisplayName()),
		openFolder: options.OpenFolder, links: map[desktopmodels.ComputerRegistration]*executor.Link{},
		sync: make(chan struct{}, 1), ctx: ctx, stop: stop, done: make(chan struct{}),
	}
	// 本机 MCP 配置、技能或运行环境变化时通知界面，并重新上报执行能力。
	changed := func() {
		options.Notify()
		if desktop.host != nil {
			desktop.host.Refresh()
		}
	}
	desktop.toolchain = toolchain.New(toolchainRoot, toolchainCache, changed)
	desktop.localMCP = localmcp.NewStore(filepath.Join(options.DataDir, localMCPConfigName), changed)
	desktop.skills = localskill.NewStore(skillDirs, changed)
	desktop.host = executor.NewHost(executor.HostOptions{
		FolderRoot: desktop.folderRoot, Toolchain: desktop.environment, MCP: desktop.localMCP, Skills: desktop.skills,
	})
	// 注册结果变化时通知界面，并增减执行器连接。
	registrar.Subscribe(func() {
		options.Notify()
		signal(desktop.sync)
	})
	return desktop
}

// signal 向容量为 1 的通道投递一次信号，已有待处理信号时直接返回。
func signal(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
