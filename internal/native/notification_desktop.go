//go:build !server && ((darwin && !ios) || windows || (linux && !android))

package native

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// notificationProvider 使用 Wails 通知服务提供桌面端原生消息提醒，点击通知后打开通知携带的页面。
type notificationProvider struct {
	openedNotification
	delivered deliveredNotifications
	service   *notifications.NotificationService
	ready     atomic.Bool
}

// notificationPathKey 是通知附加数据中页面地址的键。
const notificationPathKey = "path"

// notificationLifecycle 管理 Wails 通知服务生命周期。
type notificationLifecycle struct {
	service  *notifications.NotificationService
	provider *notificationProvider
}

// NewNotificationProvider 创建原生通知能力及其 Wails 生命周期服务。
func NewNotificationProvider() (Notifications, []application.Service) {
	service := notifications.New()
	provider := &notificationProvider{service: service}
	lifecycle := &notificationLifecycle{service: service, provider: provider}
	return provider, []application.Service{application.NewService(lifecycle)}
}

// ServiceStartup 登记通知点击回调后初始化当前系统的原生通知后端；Windows 在初始化时即交出唤起本进程的通知点击，回调须先登记；只处理默认的打开动作；Linux 的 Wails 实现把点击关闭按钮也报告为默认动作，该平台关闭通知同样会打开对应会话。
func (l *notificationLifecycle) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	l.service.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			slog.WarnContext(ctx, "读取桌面通知点击结果失败", "error", result.Error)
			return
		}
		if result.Response.ActionIdentifier != notifications.DefaultActionIdentifier {
			return
		}
		path, _ := result.Response.UserInfo[notificationPathKey].(string)
		l.provider.open(path)
	})
	if err := l.service.ServiceStartup(ctx, options); err != nil {
		l.provider.ready.Store(false)
		slog.WarnContext(ctx, "初始化桌面通知服务失败，应用将继续启动", "error", err)
		if shutdownErr := l.service.ServiceShutdown(); shutdownErr != nil {
			slog.WarnContext(ctx, "清理未完成初始化的桌面通知服务失败", "error", shutdownErr)
		}
		return nil
	}
	l.provider.ready.Store(true)
	slog.InfoContext(ctx, "桌面通知服务已初始化")
	return nil
}

// ServiceShutdown 释放当前系统的原生通知后端。
func (l *notificationLifecycle) ServiceShutdown() error {
	if !l.provider.ready.Load() {
		return nil
	}
	l.provider.ready.Store(false)
	if err := l.service.ServiceShutdown(); err != nil {
		slog.WarnContext(context.Background(), "关闭桌面通知服务失败", "error", err)
		return err
	}
	return nil
}

// CheckNotificationPermission 检查当前桌面系统的通知授权状态。
func (p *notificationProvider) CheckNotificationPermission(_ context.Context, _ appservice.RequestMeta) (NotificationPermissionStatus, error) {
	if !p.ready.Load() {
		return NotificationPermissionStatusUnsupported, nil
	}
	authorized, err := p.service.CheckNotificationAuthorization()
	if err != nil {
		slog.WarnContext(context.Background(), "检查桌面通知权限失败", "error", err)
		return "", err
	}
	// 转换桌面通知授权状态。
	if authorized {
		return NotificationPermissionStatusGranted, nil
	}
	return NotificationPermissionStatusPrompt, nil
}

// RequestNotificationPermission 请求当前桌面系统允许发送通知。
func (p *notificationProvider) RequestNotificationPermission(_ context.Context, _ appservice.RequestMeta) (NotificationPermissionStatus, error) {
	if !p.ready.Load() {
		return NotificationPermissionStatusUnsupported, nil
	}
	authorized, err := p.service.RequestNotificationAuthorization()
	if err != nil {
		slog.WarnContext(context.Background(), "申请桌面通知权限失败", "error", err)
		return "", err
	}
	// 转换桌面通知授权结果。
	status := NotificationPermissionStatusDenied
	if authorized {
		status = NotificationPermissionStatusGranted
	}
	slog.InfoContext(context.Background(), "桌面通知权限申请完成", "status", status)
	return status, nil
}

// SendMessageNotification 投递一条新消息通知，同一通知编号成功投递后在去重时长内不再投递。
func (p *notificationProvider) SendMessageNotification(_ context.Context, _ appservice.RequestMeta, input MessageNotificationInput) error {
	if !p.ready.Load() {
		return errors.New("notification service unavailable")
	}
	return p.delivered.deliver(input.ID, func() error { return p.send(input) })
}

// send 向系统通知中心投递一条新消息通知。
func (p *notificationProvider) send(input MessageNotificationInput) error {
	// 创建桌面通知参数。
	options := notifications.NotificationOptions{ID: input.ID, Title: input.Title, Body: input.Body}
	if input.Path != "" {
		options.Data = map[string]any{notificationPathKey: input.Path}
	}
	if !input.SoundEnabled {
		options.Sound = &notifications.NotificationSound{Silent: true}
	}
	err := p.service.SendNotification(options)
	if err != nil {
		slog.WarnContext(context.Background(), "投递桌面通知失败", "notification_id", input.ID, "sound_enabled", input.SoundEnabled, "error", err)
	}
	return err
}
