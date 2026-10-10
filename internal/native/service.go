//go:build !server

package native

import (
	"context"
	"sync"

	"github.com/runforyou-ai/luway/internal/appservice"
)

// ClientUpdater 由能从所连接服务器更新自身的原生端实现。
type ClientUpdater interface {
	// PrepareClientUpdate 检查服务器提供的客户端版本，较新时下载更新包并校验签名。
	PrepareClientUpdate(context.Context, appservice.RequestMeta, string) (ClientUpdate, error)
	// RestartClientUpdate 退出应用并以已准备好的新版本重新启动。
	RestartClientUpdate(context.Context, appservice.RequestMeta) error
}

// LocaleUpdater 同步当前设备上托盘、应用菜单等原生界面的语言。
type LocaleUpdater interface {
	SetLocale(appservice.Locale)
}

// ComputerHost 由把本机作为电脑的平台实现：保存前端注册得到的电脑凭据并以之执行派发给这台电脑的操作，管理为个人 AI 员工提供的本机运行环境、本地 MCP 服务与技能。
type ComputerHost interface {
	ComputerIdentity(context.Context) (ComputerIdentity, error)
	AttachedComputers(context.Context, ComputerAccount) ([]AttachedComputer, error)
	AttachComputer(context.Context, ComputerAttachment) error
	DetachComputers(context.Context, ComputerAccount, []string) error
	CurrentComputer(context.Context, ComputerAccount, string) (LocalComputer, error)
	LocalEnvironment(context.Context) (LocalEnvironment, error)
	UpdateLocalToolchain(context.Context, appservice.RequestMeta) (LocalToolchainUpdate, error)
	UninstallLocalToolchain(context.Context, appservice.RequestMeta) error
	InstallLocalToolchain(context.Context) error
	OpenLocalToolchainFolder(context.Context) error
	AddLocalMCPServer(context.Context, appservice.RequestMeta, LocalMCPServerInput) error
	RemoveLocalMCPServer(context.Context, appservice.RequestMeta, string) error
	InstallLocalSkill(context.Context, appservice.RequestMeta, LocalSkillInstallInput) error
	RemoveLocalSkill(context.Context, appservice.RequestMeta, string) error
}

// Options 定义原生端各平台提供的本机能力，当前平台不具备的能力为空。
type Options struct {
	Locale        appservice.Locale
	LocaleUpdater LocaleUpdater
	Images        *ImageSelector
	Files         *FileSaver
	Notifications Notifications
	ServerLinks   ServerLinks
	Unread        UnreadIndicator
	Windows       ConversationWindows
	Updater       ClientUpdater
	Computer      ComputerHost
}

// Service 是前端经 Wails 绑定调用的本机能力；业务数据由前端直接请求服务端。本机能力的错误文案使用界面最近同步的语言，结果中的切片均为数组。
type Service struct {
	options Options
	mu      sync.Mutex
	locale  appservice.Locale
}

// NewService 创建本机能力服务，初始语言取自系统语言。
func NewService(options Options) *Service {
	return &Service{options: options, locale: options.Locale}
}

// meta 返回以当前界面语言本地化本机能力错误的请求元数据。
func (s *Service) meta() appservice.RequestMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appservice.RequestMeta{Locale: s.locale}
}

// SetLocale 同步界面语言到托盘、应用菜单与本机能力的错误文案。
func (s *Service) SetLocale(locale string) {
	s.mu.Lock()
	s.locale = appservice.Locale(locale)
	s.mu.Unlock()
	if s.options.LocaleUpdater != nil {
		s.options.LocaleUpdater.SetLocale(appservice.Locale(locale))
	}
}

// SelectImage 打开系统文件对话框选择并读取图片，用户取消时返回空文件。
func (s *Service) SelectImage(ctx context.Context) (ImageFile, error) {
	if s.options.Images == nil {
		return ImageFile{}, appservice.UnsupportedError(ctx, s.meta(), "SelectImage")
	}
	return s.options.Images.SelectImage(ctx, s.meta())
}

// SaveTextFile 让用户选择保存位置并写入文本文件，用户取消时返回 false。
func (s *Service) SaveTextFile(ctx context.Context, input TextFileInput) (bool, error) {
	if s.options.Files == nil {
		return false, appservice.UnsupportedError(ctx, s.meta(), "SaveTextFile")
	}
	return s.options.Files.SaveTextFile(ctx, s.meta(), input)
}

// OpenConversationWindow 在独立窗口打开指定会话，同一会话已打开时聚焦现有窗口。
func (s *Service) OpenConversationWindow(ctx context.Context, input ConversationWindowInput) error {
	if s.options.Windows == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "OpenConversationWindow")
	}
	return s.options.Windows.OpenConversationWindow(ctx, s.meta(), input)
}

// CloseConversationWindows 关闭全部会话独立窗口，登录会话变化时由主窗口调用。
func (s *Service) CloseConversationWindows() {
	if s.options.Windows != nil {
		s.options.Windows.CloseAll()
	}
}

// CheckNotificationPermission 返回当前设备的系统通知授权状态。
func (s *Service) CheckNotificationPermission(ctx context.Context) (NotificationPermissionStatus, error) {
	if s.options.Notifications == nil {
		return NotificationPermissionStatusUnsupported, nil
	}
	return s.options.Notifications.CheckNotificationPermission(ctx, s.meta())
}

// RequestNotificationPermission 申请系统通知授权并返回授权状态。
func (s *Service) RequestNotificationPermission(ctx context.Context) (NotificationPermissionStatus, error) {
	if s.options.Notifications == nil {
		return NotificationPermissionStatusUnsupported, nil
	}
	return s.options.Notifications.RequestNotificationPermission(ctx, s.meta())
}

// SendMessageNotification 投递一条新消息通知。
func (s *Service) SendMessageNotification(ctx context.Context, input MessageNotificationInput) error {
	if s.options.Notifications == nil {
		return nil
	}
	return s.options.Notifications.SendMessageNotification(ctx, s.meta(), input)
}

// TakeOpenedNotificationPath 返回并清除最近一次被点击的通知要打开的页面地址，没有时返回空串。
func (s *Service) TakeOpenedNotificationPath(ctx context.Context) (string, error) {
	if s.options.Notifications == nil {
		return "", nil
	}
	return s.options.Notifications.TakeOpenedNotificationPath(ctx, s.meta())
}

// TakeOpenedServerLink 返回并清除最近一次唤起应用的连接链接携带的部署地址，没有时返回空串。
func (s *Service) TakeOpenedServerLink(ctx context.Context) (string, error) {
	if s.options.ServerLinks == nil {
		return "", nil
	}
	return s.options.ServerLinks.TakeOpenedServerLink(ctx, s.meta())
}

// UpdateUnreadIndicator 同步当前设备的未读数和托盘提醒状态。
func (s *Service) UpdateUnreadIndicator(state UnreadIndicatorState) error {
	if s.options.Unread == nil {
		return nil
	}
	return s.options.Unread.SetUnreadState(state)
}

// PrepareClientUpdate 检查服务器提供的客户端版本，较新时下载更新包并校验签名；当前端不能从服务器更新时返回 unsupported。
func (s *Service) PrepareClientUpdate(ctx context.Context, serverURL string) (ClientUpdate, error) {
	if s.options.Updater == nil {
		return ClientUpdate{State: ClientUpdateStateUnsupported}, nil
	}
	return s.options.Updater.PrepareClientUpdate(ctx, s.meta(), serverURL)
}

// RestartClientUpdate 退出应用并以已准备好的新版本重新启动。
func (s *Service) RestartClientUpdate(ctx context.Context) error {
	if s.options.Updater == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "RestartClientUpdate")
	}
	return s.options.Updater.RestartClientUpdate(ctx, s.meta())
}

// GetComputerIdentity 返回本机执行器安装标识与电脑名称；本机不作为电脑时返回空标识。
func (s *Service) GetComputerIdentity(ctx context.Context) (ComputerIdentity, error) {
	if s.options.Computer == nil {
		return ComputerIdentity{}, nil
	}
	return s.options.Computer.ComputerIdentity(ctx)
}

// ListAttachedComputers 返回本机在指定服务器上为指定账号注册的各工作区电脑。
func (s *Service) ListAttachedComputers(ctx context.Context, account ComputerAccount) ([]AttachedComputer, error) {
	if s.options.Computer == nil {
		return []AttachedComputer{}, nil
	}
	attached, err := s.options.Computer.AttachedComputers(ctx, account)
	appservice.NormalizeEmpty(&attached)
	return attached, err
}

// AttachComputer 保存前端为一个工作区注册得到的电脑凭据，并以之连接服务端执行派发给这台电脑的操作。
func (s *Service) AttachComputer(ctx context.Context, attachment ComputerAttachment) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "AttachComputer")
	}
	return s.options.Computer.AttachComputer(ctx, attachment)
}

// DetachComputers 删除本机在指定服务器上为指定账号注册、但工作区不在 keepWorkspaceIDs 中的电脑。
func (s *Service) DetachComputers(ctx context.Context, account ComputerAccount, keepWorkspaceIDs []string) error {
	if s.options.Computer == nil {
		return nil
	}
	return s.options.Computer.DetachComputers(ctx, account, keepWorkspaceIDs)
}

// CurrentComputer 返回本机在指定工作区为指定账号注册的电脑与运行环境的准备状态；本机不作为电脑时返回空电脑。
func (s *Service) CurrentComputer(ctx context.Context, account ComputerAccount, workspaceID string) (LocalComputer, error) {
	if s.options.Computer == nil {
		return LocalComputer{}, nil
	}
	return s.options.Computer.CurrentComputer(ctx, account, workspaceID)
}

// GetLocalEnvironment 返回本机为个人 AI 员工提供的运行环境、本地 MCP 服务与技能。
func (s *Service) GetLocalEnvironment(ctx context.Context) (LocalEnvironment, error) {
	if s.options.Computer == nil {
		return LocalEnvironment{}, appservice.UnsupportedError(ctx, s.meta(), "GetLocalEnvironment")
	}
	environment, err := s.options.Computer.LocalEnvironment(ctx)
	appservice.NormalizeEmpty(&environment)
	return environment, err
}

// UpdateLocalToolchain 把本机运行环境更新到下载源的最新版本。
func (s *Service) UpdateLocalToolchain(ctx context.Context) (LocalToolchainUpdate, error) {
	if s.options.Computer == nil {
		return LocalToolchainUpdate{}, appservice.UnsupportedError(ctx, s.meta(), "UpdateLocalToolchain")
	}
	return s.options.Computer.UpdateLocalToolchain(ctx, s.meta())
}

// UninstallLocalToolchain 卸载本机运行环境，手动重新安装前保持未安装。
func (s *Service) UninstallLocalToolchain(ctx context.Context) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "UninstallLocalToolchain")
	}
	return s.options.Computer.UninstallLocalToolchain(ctx, s.meta())
}

// InstallLocalToolchain 重新安装已卸载的本机运行环境。
func (s *Service) InstallLocalToolchain(ctx context.Context) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "InstallLocalToolchain")
	}
	return s.options.Computer.InstallLocalToolchain(ctx)
}

// OpenLocalToolchainFolder 在系统文件管理器中打开本机运行环境的安装位置。
func (s *Service) OpenLocalToolchainFolder(ctx context.Context) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "OpenLocalToolchainFolder")
	}
	return s.options.Computer.OpenLocalToolchainFolder(ctx)
}

// AddLocalMCPServer 试启动并添加这台电脑上的本地 MCP 服务，同名服务被替换。
func (s *Service) AddLocalMCPServer(ctx context.Context, input LocalMCPServerInput) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "AddLocalMCPServer")
	}
	return s.options.Computer.AddLocalMCPServer(ctx, s.meta(), input)
}

// RemoveLocalMCPServer 删除这台电脑上的本地 MCP 服务。
func (s *Service) RemoveLocalMCPServer(ctx context.Context, name string) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "RemoveLocalMCPServer")
	}
	return s.options.Computer.RemoveLocalMCPServer(ctx, s.meta(), name)
}

// InstallLocalSkill 从来源把技能安装到这台电脑，同名技能被替换。
func (s *Service) InstallLocalSkill(ctx context.Context, input LocalSkillInstallInput) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "InstallLocalSkill")
	}
	return s.options.Computer.InstallLocalSkill(ctx, s.meta(), input)
}

// RemoveLocalSkill 删除 AI 员工安装在这台电脑上的技能。
func (s *Service) RemoveLocalSkill(ctx context.Context, name string) error {
	if s.options.Computer == nil {
		return appservice.UnsupportedError(ctx, s.meta(), "RemoveLocalSkill")
	}
	return s.options.Computer.RemoveLocalSkill(ctx, s.meta(), name)
}
