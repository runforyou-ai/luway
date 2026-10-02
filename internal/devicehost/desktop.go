//go:build !server && !ios && !android

package devicehost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/localmcp"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
	"github.com/runforyou-ai/luway/internal/integration/toolchain"
)

// localMCPConfigName 是数据目录中本地 MCP 配置文件的名称。
const localMCPConfigName = "mcp.json"

// DesktopClient 定义桌面端设备注册与运行执行共用的服务端调用。
type DesktopClient interface {
	Client
	RunClient
}

// DesktopOptions 定义桌面端设备使用的数据目录、界面通知与系统文件管理器。
type DesktopOptions struct {
	DataDir    string
	Notify     func()
	OpenFolder func(string) error
}

// Desktop 组合桌面端本机设备注册、Agent 运行执行循环、运行环境、本地 MCP 服务配置与技能目录。
type Desktop struct {
	*Registrar
	worker     *Worker
	toolchain  *toolchain.Manager
	localMCP   *localmcp.Store
	skills     *localskill.Store
	openFolder func(string) error
}

// CurrentDevice 返回本机设备注册状态与 Agent 运行环境的准备状态。
func (d *Desktop) CurrentDevice(ctx context.Context, meta appservice.RequestMeta) (appservice.LocalDevice, error) {
	device, err := d.Registrar.CurrentDevice(ctx, meta)
	if err != nil {
		return device, err
	}
	local := d.toolchainStatus()
	device.Toolchain = &local
	return device, nil
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
	slog.Warn("更新 Agent 运行环境失败", "error", err)
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
	slog.Warn("卸载 Agent 运行环境失败", "error", err)
	return appservice.FailedError(meta, i18n.ErrorLocalToolchainUninstall)
}

// InstallLocalToolchain 清除卸载记录并在后台重新安装运行环境。
func (d *Desktop) InstallLocalToolchain(context.Context, appservice.RequestMeta) error {
	if err := d.toolchain.Install(); err != nil {
		return err
	}
	// 重新安装期间设备不领取运行，安装完成后经 Wake 重新检查。
	d.worker.Wake()
	return nil
}

// OpenLocalToolchainFolder 在系统文件管理器中打开运行环境的安装位置。
func (d *Desktop) OpenLocalToolchainFolder(context.Context, appservice.RequestMeta) error {
	root := d.toolchain.Info().Root
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return d.openFolder(root)
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

// Start 开始设备注册与执行循环。
func (d *Desktop) Start() {
	d.Registrar.Start()
	d.worker.Start()
}

// Stop 结束执行循环与设备注册并等待其退出。
func (d *Desktop) Stop() {
	d.worker.Stop()
	d.Registrar.Stop()
}

// NewDesktop 创建桌面端本机设备注册与执行循环，各会话的默认文件夹位于用户文档目录下以产品名称命名的文件夹；当前平台不注册设备或本机运行时创建失败时返回 nil。
func NewDesktop(store Store, client DesktopClient, sessions *clientsession.Manager, options DesktopOptions) *Desktop {
	registrar := New(store, client, sessions)
	if registrar == nil {
		return nil
	}
	runtime, err := agentruntime.New()
	if err != nil {
		slog.Error("创建本机 Agent 运行时失败，本机设备不注册", "error", err)
		return nil
	}
	// 无法确定文档目录时默认文件夹放在系统临时目录下，个人 AI 员工照常运行。
	documents, err := DocumentsDir()
	if err != nil {
		slog.Warn("无法确定用户文档目录，会话默认文件夹改放在临时目录", "error", err)
		documents = os.TempDir()
	}
	toolchainRoot, toolchainCache, err := toolchain.DefaultDirs()
	if err != nil {
		slog.Error("无法确定 Agent 运行环境目录，本机设备不注册", "error", err)
		return nil
	}
	registrar.Subscribe(options.Notify)
	// 运行环境准备结束后立即重新检查待领取运行。
	var worker *Worker
	runEnvironment := toolchain.New(toolchainRoot, toolchainCache, func() {
		options.Notify()
		worker.Wake()
	})
	skillDirs, err := localskill.DefaultDirs()
	if err != nil {
		slog.Error("无法确定技能目录，本机设备不注册", "error", err)
		return nil
	}
	localMCP := localmcp.NewStore(filepath.Join(options.DataDir, localMCPConfigName), options.Notify)
	skills := localskill.NewStore(skillDirs, options.Notify)
	worker = NewWorker(registrar, client, runtime, runEnvironment, localMCP, skills, filepath.Join(documents, brand.Build().DisplayName()), filepath.Join(toolchainRoot, "local-agents"))
	return &Desktop{Registrar: registrar, worker: worker, toolchain: runEnvironment, localMCP: localMCP, skills: skills, openFolder: options.OpenFolder}
}

// Worker 返回本机运行执行循环。
func (d *Desktop) Worker() *Worker {
	return d.worker
}
