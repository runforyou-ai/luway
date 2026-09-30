package appservice

import (
	"context"
	"strings"

	"github.com/runforyou-ai/luway/internal/i18n"
)

// Service 将跨平台业务调用转发给当前运行平台的 Backend。
//
// Service 是各端业务调用的统一出口，每个带结果的方法都归一化结果中的 nil 切片。
type Service struct {
	backend             Backend
	imageSelector       ImageSelector
	nativeLocaleUpdater NativeLocaleUpdater
	nativeNotification  NativeNotification
	nativeServerLink    NativeServerLink
	unreadIndicator     UnreadIndicator
	conversationWindows ConversationWindowOpener
	localDevice         LocalDeviceReporter
	localEnvironment    LocalEnvironmentManager
}

// Option 配置平台专属的应用服务能力。
type Option func(*Service)

// WithImageSelector 注入原生端图片文件选择器。
func WithImageSelector(selector ImageSelector) Option {
	return func(service *Service) {
		service.imageSelector = selector
	}
}

// WithNativeLocaleUpdater 注入原生界面语言同步能力。
func WithNativeLocaleUpdater(updater NativeLocaleUpdater) Option {
	return func(service *Service) {
		service.nativeLocaleUpdater = updater
	}
}

// WithNativeNotification 注入原生端系统通知能力。
func WithNativeNotification(notification NativeNotification) Option {
	return func(service *Service) {
		service.nativeNotification = notification
	}
}

// WithNativeServerLink 注入原生端连接链接接收能力。
func WithNativeServerLink(link NativeServerLink) Option {
	return func(service *Service) {
		service.nativeServerLink = link
	}
}

// WithUnreadIndicator 注入原生端未读提示能力。
func WithUnreadIndicator(indicator UnreadIndicator) Option {
	return func(service *Service) {
		service.unreadIndicator = indicator
	}
}

// WithConversationWindowOpener 注入桌面端会话独立窗口能力。
func WithConversationWindowOpener(opener ConversationWindowOpener) Option {
	return func(service *Service) {
		service.conversationWindows = opener
	}
}

// WithLocalDevice 注入原生端本机设备注册状态。
func WithLocalDevice(reporter LocalDeviceReporter) Option {
	return func(service *Service) {
		service.localDevice = reporter
	}
}

// WithLocalEnvironment 注入原生端为助理提供的本机运行环境与本地 MCP 服务管理。
func WithLocalEnvironment(manager LocalEnvironmentManager) Option {
	return func(service *Service) {
		service.localEnvironment = manager
	}
}

// New 创建跨平台应用服务。
func New(backend Backend, options ...Option) *Service {
	service := &Service{backend: backend}
	for _, option := range options {
		option(service)
	}
	return service
}

// InstallWorkspace 完成首次安装并返回部署管理员的登录会话。
func (s *Service) InstallWorkspace(ctx context.Context, meta RequestMeta, input InstallWorkspaceInput) (Auth, error) {
	installer, ok := s.backend.(WorkspaceInstaller)
	if !ok {
		return Auth{}, methodNotAllowedError(meta, "InstallWorkspace")
	}
	return WithNormalizedSlices(installer.InstallWorkspace(ctx, meta, input))
}

// Login 校验账号密码并建立登录会话。
func (s *Service) Login(ctx context.Context, meta RequestMeta, input LoginInput) (Auth, error) {
	auth, err := s.backend.Login(ctx, meta, input)
	if err != nil {
		return Auth{}, err
	}
	s.setNativeLocale(auth.Account.Locale)
	return WithNormalizedSlices(auth, nil)
}

// Register 注册本地账号并建立登录会话。
func (s *Service) Register(ctx context.Context, meta RequestMeta, input RegisterInput) (Auth, error) {
	auth, err := s.backend.Register(ctx, meta, input)
	if err != nil {
		return Auth{}, err
	}
	s.setNativeLocale(auth.Account.Locale)
	return WithNormalizedSlices(auth, nil)
}

// CompleteOfficialLogin 用授权码完成官方账号登录并建立登录会话。
func (s *Service) CompleteOfficialLogin(ctx context.Context, meta RequestMeta, input OfficialLoginCompletion) (Auth, error) {
	auth, err := s.backend.CompleteOfficialLogin(ctx, meta, input)
	if err != nil {
		return Auth{}, err
	}
	s.setNativeLocale(auth.Account.Locale)
	return WithNormalizedSlices(auth, nil)
}

// LoadIdentity 返回当前账号在请求目标工作区中的成员身份。
func (s *Service) LoadIdentity(ctx context.Context, meta RequestMeta) (Identity, error) {
	identity, err := s.backend.LoadIdentity(ctx, meta)
	if err != nil {
		return Identity{}, err
	}
	s.setNativeLocale(identity.User.Locale)
	return WithNormalizedSlices(identity, nil)
}

// UpdateUserPreferences 保存当前用户的偏好设置。
func (s *Service) UpdateUserPreferences(ctx context.Context, meta RequestMeta, input UserPreferencesInput) (CurrentUser, error) {
	user, err := s.backend.UpdateUserPreferences(ctx, meta, input)
	if err != nil {
		return CurrentUser{}, err
	}
	s.setNativeLocale(user.Locale)
	return WithNormalizedSlices(user, nil)
}

// setNativeLocale 在当前平台支持时同步原生界面语言。
func (s *Service) setNativeLocale(locale Locale) {
	if s.nativeLocaleUpdater != nil {
		s.nativeLocaleUpdater.SetLocale(locale)
	}
}

// SelectImage 在原生端选择并读取图片。
func (s *Service) SelectImage(ctx context.Context, meta RequestMeta) (ImageFile, error) {
	if s.imageSelector == nil {
		return ImageFile{}, methodNotAllowedError(meta, "SelectImage")
	}
	return WithNormalizedSlices(s.imageSelector.SelectImage(ctx, meta))
}

// OpenConversationWindow 在桌面端独立窗口打开指定会话，同一会话已打开时聚焦现有窗口。
func (s *Service) OpenConversationWindow(ctx context.Context, meta RequestMeta, input ConversationWindowInput) error {
	if s.conversationWindows == nil {
		return methodNotAllowedError(meta, "OpenConversationWindow")
	}
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.Title = strings.TrimSpace(input.Title)
	if input.ConversationID == "" {
		return InvalidError(meta, i18n.FieldConversationIDInvalid, nil)
	}
	return s.conversationWindows.OpenConversationWindow(ctx, meta, input)
}

// CheckNotificationPermission 返回当前设备的系统通知授权状态。
func (s *Service) CheckNotificationPermission(ctx context.Context, meta RequestMeta) (NotificationPermissionStatus, error) {
	if s.nativeNotification == nil {
		return NotificationPermissionStatusUnsupported, nil
	}
	return s.nativeNotification.CheckNotificationPermission(ctx, meta)
}

// RequestNotificationPermission 请求当前设备允许发送系统通知。
func (s *Service) RequestNotificationPermission(ctx context.Context, meta RequestMeta) (NotificationPermissionStatus, error) {
	if s.nativeNotification == nil {
		return NotificationPermissionStatusUnsupported, nil
	}
	return s.nativeNotification.RequestNotificationPermission(ctx, meta)
}

// SendMessageNotification 在当前设备投递一条新消息系统通知。
func (s *Service) SendMessageNotification(ctx context.Context, meta RequestMeta, input MessageNotificationInput) error {
	if s.nativeNotification == nil {
		return methodNotAllowedError(meta, "SendMessageNotification")
	}
	return s.nativeNotification.SendMessageNotification(ctx, meta, input)
}

// TakeOpenedNotificationPath 返回并清除最近一次被点击的系统通知要打开的页面地址；没有待打开的页面或当前端不投递原生通知时返回空串。
func (s *Service) TakeOpenedNotificationPath(ctx context.Context, meta RequestMeta) (string, error) {
	if s.nativeNotification == nil {
		return "", nil
	}
	return s.nativeNotification.TakeOpenedNotificationPath(ctx, meta)
}

// TakeOpenedServerLink 返回并清除最近一次唤起应用的连接链接携带的部署地址；没有待处理的链接或当前端不接收连接链接时返回空串。
func (s *Service) TakeOpenedServerLink(ctx context.Context, meta RequestMeta) (string, error) {
	if s.nativeServerLink == nil {
		return "", nil
	}
	return s.nativeServerLink.TakeOpenedServerLink(ctx, meta)
}

// UpdateUnreadIndicator 更新当前设备的未读提示。
func (s *Service) UpdateUnreadIndicator(_ context.Context, meta RequestMeta, state UnreadIndicatorState) error {
	if s.unreadIndicator == nil {
		return methodNotAllowedError(meta, "UpdateUnreadIndicator")
	}
	return s.unreadIndicator.SetUnreadState(state)
}

// CurrentDevice 返回本机在当前企业服务器上的设备注册状态与 Agent 运行环境的准备状态；不注册设备的平台返回空设备编号且不含运行环境。
func (s *Service) CurrentDevice(ctx context.Context, meta RequestMeta) (LocalDevice, error) {
	if s.localDevice == nil {
		return LocalDevice{}, nil
	}
	return WithNormalizedSlices(s.localDevice.CurrentDevice(ctx, meta))
}

// GetLocalEnvironment 返回本机为助理提供的运行环境、本地 MCP 服务与技能。
func (s *Service) GetLocalEnvironment(ctx context.Context, meta RequestMeta) (LocalEnvironment, error) {
	if s.localEnvironment == nil {
		return LocalEnvironment{}, methodNotAllowedError(meta, "GetLocalEnvironment")
	}
	return WithNormalizedSlices(s.localEnvironment.LocalEnvironment(ctx, meta))
}

// UpdateLocalToolchain 把本机运行环境更新到下载源的最新版本。
func (s *Service) UpdateLocalToolchain(ctx context.Context, meta RequestMeta) (LocalToolchainUpdate, error) {
	if s.localEnvironment == nil {
		return LocalToolchainUpdate{}, methodNotAllowedError(meta, "UpdateLocalToolchain")
	}
	return WithNormalizedSlices(s.localEnvironment.UpdateLocalToolchain(ctx, meta))
}

// UninstallLocalToolchain 卸载本机运行环境，重新安装前不再自动安装。
func (s *Service) UninstallLocalToolchain(ctx context.Context, meta RequestMeta) error {
	if s.localEnvironment == nil {
		return methodNotAllowedError(meta, "UninstallLocalToolchain")
	}
	return s.localEnvironment.UninstallLocalToolchain(ctx, meta)
}

// InstallLocalToolchain 重新安装已卸载的本机运行环境。
func (s *Service) InstallLocalToolchain(ctx context.Context, meta RequestMeta) error {
	if s.localEnvironment == nil {
		return methodNotAllowedError(meta, "InstallLocalToolchain")
	}
	return s.localEnvironment.InstallLocalToolchain(ctx, meta)
}

// OpenLocalToolchainFolder 在系统文件管理器中打开本机运行环境的安装位置。
func (s *Service) OpenLocalToolchainFolder(ctx context.Context, meta RequestMeta) error {
	if s.localEnvironment == nil {
		return methodNotAllowedError(meta, "OpenLocalToolchainFolder")
	}
	return s.localEnvironment.OpenLocalToolchainFolder(ctx, meta)
}

// RemoveLocalMCPServer 删除这台电脑上的本地 MCP 服务。
func (s *Service) RemoveLocalMCPServer(ctx context.Context, meta RequestMeta, name string) error {
	if s.localEnvironment == nil {
		return methodNotAllowedError(meta, "RemoveLocalMCPServer")
	}
	return s.localEnvironment.RemoveLocalMCPServer(ctx, meta, name)
}

// RemoveLocalSkill 删除助理安装在这台电脑上的技能。
func (s *Service) RemoveLocalSkill(ctx context.Context, meta RequestMeta, name string) error {
	if s.localEnvironment == nil {
		return methodNotAllowedError(meta, "RemoveLocalSkill")
	}
	return s.localEnvironment.RemoveLocalSkill(ctx, meta, name)
}

// ServerURL 返回原生端当前配置的企业服务器地址。
func (s *Service) ServerURL(ctx context.Context, meta RequestMeta) (string, error) {
	connector, ok := s.backend.(ServerConnector)
	if !ok {
		return "", methodNotAllowedError(meta, "ServerURL")
	}
	return connector.ServerURL(ctx, meta)
}

// ProbeServer 检测企业服务器并返回公开企业名称。
func (s *Service) ProbeServer(ctx context.Context, meta RequestMeta, serverURL string) (InstallationStatus, error) {
	connector, ok := s.backend.(ServerConnector)
	if !ok {
		return InstallationStatus{}, methodNotAllowedError(meta, "ProbeServer")
	}
	return WithNormalizedSlices(connector.ProbeServer(ctx, meta, serverURL))
}

// ConnectServer 验证并保存原生端企业服务器地址。
func (s *Service) ConnectServer(ctx context.Context, meta RequestMeta, serverURL string) error {
	connector, ok := s.backend.(ServerConnector)
	if !ok {
		return methodNotAllowedError(meta, "ConnectServer")
	}
	return connector.ConnectServer(ctx, meta, serverURL)
}

// ConnectRealtime 在原生端使用当前登录凭据建立实时事件流，服务端事件与事件流结束经 Wails 事件投递。
func (s *Service) ConnectRealtime(ctx context.Context, meta RequestMeta) (RealtimeConnection, error) {
	connector, ok := s.backend.(RealtimeConnector)
	if !ok {
		return RealtimeConnection{}, methodNotAllowedError(meta, "ConnectRealtime")
	}
	return WithNormalizedSlices(connector.ConnectRealtime(ctx, meta))
}

// DisconnectRealtime 关闭原生端指定编号的成员实时事件流及其所属窗口的全部运行过程流。
func (s *Service) DisconnectRealtime(ctx context.Context, meta RequestMeta, connectionID string) error {
	connector, ok := s.backend.(RealtimeConnector)
	if !ok {
		return methodNotAllowedError(meta, "DisconnectRealtime")
	}
	return connector.DisconnectRealtime(ctx, meta, connectionID)
}

// ConnectAgentRunStream 在原生端建立指定运行的过程流，运行过程事件与流结束经 Wails 事件投递。
func (s *Service) ConnectAgentRunStream(ctx context.Context, meta RequestMeta, runID string) (RealtimeConnection, error) {
	connector, ok := s.backend.(RealtimeConnector)
	if !ok {
		return RealtimeConnection{}, methodNotAllowedError(meta, "ConnectAgentRunStream")
	}
	return WithNormalizedSlices(connector.ConnectAgentRunStream(ctx, meta, runID))
}

// ConnectWorkspaceActivity 在原生端使用当前登录凭据建立工作区动态事件流，事件与流结束经 Wails 事件投递。
func (s *Service) ConnectWorkspaceActivity(ctx context.Context, meta RequestMeta) (RealtimeConnection, error) {
	connector, ok := s.backend.(RealtimeConnector)
	if !ok {
		return RealtimeConnection{}, methodNotAllowedError(meta, "ConnectWorkspaceActivity")
	}
	return WithNormalizedSlices(connector.ConnectWorkspaceActivity(ctx, meta))
}

// DisconnectWorkspaceActivity 关闭原生端指定本地流编号的工作区动态事件流。
func (s *Service) DisconnectWorkspaceActivity(ctx context.Context, meta RequestMeta, connectionID string) error {
	connector, ok := s.backend.(RealtimeConnector)
	if !ok {
		return methodNotAllowedError(meta, "DisconnectWorkspaceActivity")
	}
	return connector.DisconnectWorkspaceActivity(ctx, meta, connectionID)
}

// DisconnectAgentRunStream 关闭原生端指定本地流编号的运行过程流。
func (s *Service) DisconnectAgentRunStream(ctx context.Context, meta RequestMeta, connectionID string) error {
	connector, ok := s.backend.(RealtimeConnector)
	if !ok {
		return methodNotAllowedError(meta, "DisconnectAgentRunStream")
	}
	return connector.DisconnectAgentRunStream(ctx, meta, connectionID)
}
