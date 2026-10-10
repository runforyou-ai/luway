//go:build !server && !ios && !android

package computerhost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/executor"
	"github.com/runforyou-ai/luway/internal/executor/localmcp"
	"github.com/runforyou-ai/luway/internal/executor/localskill"
	"github.com/runforyou-ai/luway/internal/executor/localworkspace"
	"github.com/runforyou-ai/luway/internal/executor/toolchain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/native"
	desktopstorage "github.com/runforyou-ai/luway/internal/storage/desktop"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
)

const (
	// maxNameRunes 是上报电脑名称的最大字符数，与服务端校验上限一致；超长主机名截断后上报。
	maxNameRunes = 100
	// checkInterval 是重新检查运行环境与注册结果的间隔，运行环境准备失败后据此按退避重试。
	checkInterval = 30 * time.Second
)

// DesktopOptions 定义桌面端电脑使用的数据目录、界面通知与系统文件管理器。
type DesktopOptions struct {
	DataDir    string
	Notify     func()
	OpenFolder func(string) error
}

// Desktop 组合桌面端电脑注册结果、执行器连接、运行环境、本地 MCP 服务配置与技能目录；电脑由前端以登录会话注册，本机只保存电脑凭据并以之连接服务端。
type Desktop struct {
	computers  *desktopstorage.Computers
	name       string
	notify     func()
	host       *executor.Host
	toolchain  *toolchain.Manager
	localMCP   *localmcp.Store
	skills     *localskill.Store
	folderRoot string
	openFolder func(string) error

	mu    sync.Mutex
	links map[desktopstorage.ComputerRegistration]*executor.Link
	sync  chan struct{}
	ctx   context.Context
	stop  context.CancelFunc
	done  chan struct{}
}

// ComputerIdentity 返回本机执行器安装标识与电脑名称。
func (d *Desktop) ComputerIdentity(context.Context) (native.ComputerIdentity, error) {
	installID, err := d.computers.InstallID()
	return native.ComputerIdentity{InstallID: installID, Name: d.name}, err
}

// AttachedComputers 返回本机在指定服务器上为指定账号注册的各工作区电脑。
func (d *Desktop) AttachedComputers(_ context.Context, account native.ComputerAccount) ([]native.AttachedComputer, error) {
	registrations, err := d.computers.List()
	return arr.FilterMap(registrations, func(registration desktopstorage.ComputerRegistration) (native.AttachedComputer, bool) {
		return native.AttachedComputer{WorkspaceID: registration.WorkspaceID, ComputerID: registration.ComputerID},
			registration.ServerURL == account.ServerURL && registration.AccountID == account.AccountID
	}), err
}

// AttachComputer 保存前端为一个工作区注册得到的电脑凭据，并为它建立执行器连接。
func (d *Desktop) AttachComputer(ctx context.Context, attachment native.ComputerAttachment) error {
	if err := d.computers.Save(desktopstorage.ComputerRegistration{
		ServerURL: attachment.ServerURL, AccountID: attachment.AccountID, WorkspaceID: attachment.WorkspaceID,
		ComputerID: attachment.ComputerID, Credential: attachment.Credential,
	}); err != nil {
		return err
	}
	slog.InfoContext(logscope.WithMember(ctx, attachment.WorkspaceID, attachment.AccountID), "电脑已注册", "server_url", attachment.ServerURL, "computer_id", attachment.ComputerID)
	d.changed()
	return nil
}

// DetachComputers 删除本机在指定服务器上为指定账号注册、但工作区不在 keepWorkspaceIDs 中的电脑，并结束它们的执行器连接。
func (d *Desktop) DetachComputers(ctx context.Context, account native.ComputerAccount, keepWorkspaceIDs []string) error {
	removed, err := d.computers.Delete(func(registration desktopstorage.ComputerRegistration) bool {
		return registration.ServerURL == account.ServerURL && registration.AccountID == account.AccountID && !slices.Contains(keepWorkspaceIDs, registration.WorkspaceID)
	})
	if err != nil {
		return err
	}
	if removed {
		slog.InfoContext(logscope.WithAccount(ctx, account.AccountID), "账号已不在工作区中，删除电脑注册结果", "server_url", account.ServerURL)
		d.changed()
	}
	return nil
}

// CurrentComputer 返回本机在指定工作区为指定账号注册的电脑与运行环境的准备状态，尚未注册时电脑编号为空。
func (d *Desktop) CurrentComputer(_ context.Context, account native.ComputerAccount, workspaceID string) (native.LocalComputer, error) {
	registrations, err := d.computers.List()
	if err != nil {
		return native.LocalComputer{}, err
	}
	computer := native.LocalComputer{Toolchain: new(d.toolchainStatus())}
	for _, registration := range registrations {
		if registration.ServerURL == account.ServerURL && registration.AccountID == account.AccountID && registration.WorkspaceID == workspaceID {
			computer.ComputerID = registration.ComputerID
		}
	}
	return computer, nil
}

// forget 删除凭据已失效的注册结果并结束其执行器连接；前端在下一次登录会话中重新注册该工作区。
func (d *Desktop) forget(registration desktopstorage.ComputerRegistration) {
	ctx := logscope.WithWorkspace(context.Background(), registration.WorkspaceID)
	removed, err := d.computers.Delete(func(existing desktopstorage.ComputerRegistration) bool { return existing == registration })
	if err != nil {
		slog.WarnContext(ctx, "删除失效的电脑注册结果失败", "server_url", registration.ServerURL, "error", err)
		return
	}
	if removed {
		slog.InfoContext(ctx, "电脑凭据已失效，删除本机注册结果", "server_url", registration.ServerURL, "computer_id", registration.ComputerID)
		d.changed()
	}
}

// changed 通知界面本机电脑已变化，并按注册结果增减执行器连接。
func (d *Desktop) changed() {
	d.notify()
	signal(d.sync)
}

// LocalEnvironment 返回运行环境的状态、安装位置、各组件版本、本地 MCP 服务与技能。
func (d *Desktop) LocalEnvironment(ctx context.Context) (native.LocalEnvironment, error) {
	info := d.toolchain.Info()
	servers, err := d.localMCP.List()
	if err != nil {
		return native.LocalEnvironment{}, err
	}
	environment := native.LocalEnvironment{
		Toolchain: d.toolchainStatus(), Location: info.Root, UVVersion: info.UV, NodeVersion: info.Node, PythonVersion: info.Python,
		MCPServers: make([]native.LocalMCPServer, 0, len(servers)),
	}
	for _, server := range servers {
		environment.MCPServers = append(environment.MCPServers, native.LocalMCPServer{
			Name: server.Name, Type: native.LocalMCPServerType(server.Transport()), Command: server.Command, Args: server.Args, URL: server.URL,
		})
	}
	skills, err := d.skills.List(ctx)
	if err != nil {
		return native.LocalEnvironment{}, err
	}
	environment.Skills = make([]native.LocalSkill, len(skills))
	for i, skill := range skills {
		environment.Skills[i] = native.LocalSkill{
			Name: skill.Name, Description: skill.Description, Source: native.LocalSkillSource(skill.Source), Location: skill.Dir,
		}
	}
	return environment, nil
}

// UpdateLocalToolchain 把运行环境更新到下载源的最新版本，失败原因转换为本地化错误。
func (d *Desktop) UpdateLocalToolchain(ctx context.Context, meta appservice.RequestMeta) (native.LocalToolchainUpdate, error) {
	updated, err := d.toolchain.Update(ctx)
	switch {
	case err == nil:
		return native.LocalToolchainUpdate{Updated: updated}, nil
	case errors.Is(err, toolchain.ErrBusy):
		return native.LocalToolchainUpdate{}, appservice.ConflictError(meta, i18n.ErrorLocalToolchainBusy, "toolchain_busy")
	case errors.Is(err, toolchain.ErrNotReady):
		return native.LocalToolchainUpdate{}, appservice.ConflictError(meta, i18n.ErrorLocalToolchainNotReady, "toolchain_not_ready")
	}
	slog.WarnContext(ctx, "更新运行环境失败", "error", err)
	key := map[toolchain.Failure]i18n.Key{
		toolchain.FailureDownload: i18n.ErrorLocalToolchainDownload,
		toolchain.FailureVerify:   i18n.ErrorLocalToolchainVerify,
		toolchain.FailureInstall:  i18n.ErrorLocalToolchainInstall,
	}[toolchain.FailureOf(err)]
	return native.LocalToolchainUpdate{}, appservice.FailedError(meta, key, err)
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
	slog.WarnContext(context.Background(), "卸载运行环境失败", "error", err)
	return appservice.FailedError(meta, i18n.ErrorLocalToolchainUninstall, err)
}

// InstallLocalToolchain 清除卸载记录并在后台重新安装运行环境。
func (d *Desktop) InstallLocalToolchain(context.Context) error {
	return d.toolchain.Install()
}

// OpenLocalToolchainFolder 在系统文件管理器中打开运行环境的安装位置。
func (d *Desktop) OpenLocalToolchainFolder(context.Context) error {
	root := d.toolchain.Info().Root
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return d.openFolder(root)
}

// AddLocalMCPServer 在运行环境中试启动本地 MCP 服务并读取工具目录，成功后保存配置，同名服务被替换；试启动失败时返回原因与服务的错误输出。
func (d *Desktop) AddLocalMCPServer(ctx context.Context, meta appservice.RequestMeta, input native.LocalMCPServerInput) error {
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
		failure := appservice.FailedError(meta, i18n.ErrorLocalMCPServerStartFailed, err)
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
func (d *Desktop) InstallLocalSkill(ctx context.Context, meta appservice.RequestMeta, input native.LocalSkillInstallInput) error {
	// 安装失败时在本地化说明后附上原因。
	if _, err := d.skills.Install(ctx, strings.TrimSpace(input.Source), strings.TrimSpace(input.Name)); err != nil {
		failure := appservice.FailedError(meta, i18n.ErrorLocalSkillInstallFailed, err)
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
func (d *Desktop) toolchainStatus() native.LocalToolchain {
	status := d.toolchain.Status()
	local := native.LocalToolchain{State: native.LocalToolchainState(status.State), Updating: status.Updating}
	if status.Failure != "" {
		local.Failure = new(native.LocalToolchainFailure(status.Failure))
	}
	return local
}

// environment 返回命令与本机 MCP 服务使用的运行环境，以及托管的 uv、Node.js 与 Python 是否可用。
func (d *Desktop) environment() (localworkspace.Environment, bool) {
	environment := d.toolchain.Environment()
	return environment, len(environment.PathPrefix) > 0
}

// Start 开始准备运行环境与执行器连接。
func (d *Desktop) Start() {
	go d.run()
	slog.InfoContext(context.Background(), "本机电脑已启动", "folder_root", d.folderRoot)
}

// Stop 结束执行器连接并等待其退出。
func (d *Desktop) Stop() {
	d.stop()
	<-d.done
	d.toolchain.Close()
	d.host.Close()
	slog.InfoContext(context.Background(), "本机电脑已停止")
}

// run 定期准备运行环境，并在注册结果变化时按本机全部注册结果增减执行器连接，直到停止。
func (d *Desktop) run() {
	defer close(d.done)
	defer func() {
		d.mu.Lock()
		links := d.links
		d.links = map[desktopstorage.ComputerRegistration]*executor.Link{}
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
	ctx := d.ctx
	registrations, err := d.computers.List()
	if err != nil {
		slog.WarnContext(ctx, "读取电脑注册结果失败", "error", err)
		return
	}
	current := make(map[desktopstorage.ComputerRegistration]bool, len(registrations))
	for _, registration := range registrations {
		current[registration] = true
		d.mu.Lock()
		_, running := d.links[registration]
		d.mu.Unlock()
		if running {
			continue
		}
		link, err := executor.StartLink(executor.LinkOptions{
			ServerURL: registration.ServerURL, Credential: registration.Credential, Host: d.host,
			OnCredentialInvalid: func() { d.forget(registration) },
		})
		if err != nil {
			slog.WarnContext(logscope.WithWorkspace(ctx, registration.WorkspaceID), "建立执行器连接失败", "server_url", registration.ServerURL, "error", err)
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

// NewDesktop 创建桌面端电脑与执行器，电脑注册保存在数据目录，各会话的默认文件夹位于用户文档目录下以产品名称命名的文件夹；当前平台不作为电脑或本机目录无法确定时返回 nil。
func NewDesktop(options DesktopOptions) *Desktop {
	if _, ok := executor.Platform(); !ok {
		slog.InfoContext(context.Background(), "当前平台不注册为电脑", "os", runtime.GOOS)
		return nil
	}
	computers, err := desktopstorage.OpenComputers(options.DataDir)
	if err != nil {
		slog.ErrorContext(context.Background(), "无法打开电脑注册文件，这台电脑不注册", "error", err)
		return nil
	}
	// 无法确定文档目录时默认文件夹放在系统临时目录下，执行照常进行。
	documents, err := DocumentsDir()
	if err != nil {
		slog.WarnContext(context.Background(), "无法确定用户文档目录，会话默认文件夹改放在临时目录", "error", err)
		documents = os.TempDir()
	}
	toolchainRoot, toolchainCache, err := toolchain.DefaultDirs()
	if err != nil {
		slog.ErrorContext(context.Background(), "无法确定运行环境目录，这台电脑不注册", "error", err)
		return nil
	}
	skillDirs, err := localskill.DefaultDirs()
	if err != nil {
		slog.ErrorContext(context.Background(), "无法确定技能目录，这台电脑不注册", "error", err)
		return nil
	}
	ctx, stop := context.WithCancel(context.Background())
	desktop := &Desktop{
		computers: computers, name: computerName(), notify: options.Notify, folderRoot: filepath.Join(documents, brand.Build().DisplayName()),
		openFolder: options.OpenFolder, links: map[desktopstorage.ComputerRegistration]*executor.Link{},
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
	hostOptions := executor.NewHostOptions(executor.LocalConfig{
		FolderRoot: desktop.folderRoot, DataDir: options.DataDir, SkillDirs: skillDirs, Toolchain: desktop.environment, OnChange: changed,
	})
	desktop.localMCP, desktop.skills = hostOptions.MCP, hostOptions.Skills
	desktop.host = executor.NewHost(hostOptions)
	return desktop
}

// computerName 返回本机名称，读取失败时使用平台名称，超出服务端上限时截断。
func computerName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		slog.WarnContext(context.Background(), "读取本机名称失败", "error", err)
		return runtime.GOOS
	}
	return str.Substr(hostname, 0, maxNameRunes)
}

// signal 向容量为 1 的通道投递一次信号，已有待处理信号时直接返回。
func signal(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
