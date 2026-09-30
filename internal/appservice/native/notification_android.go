//go:build !server && android

package native

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// notificationResultEvent 是原生通知桥接回报结果使用的事件名。
const notificationResultEvent = "app:notification"

// notificationCallTimeout 是等待原生通知桥接回报的最长时间，权限申请包含用户操作耗时。
const notificationCallTimeout = 2 * time.Minute

// notificationResult 表示原生通知桥接一次调用的结果。
type notificationResult struct {
	ok         bool
	permission appservice.NotificationPermissionStatus
}

// notificationProvider 通过 Android 通知桥接提供本地通知权限和消息投递，点击通知后打开通知携带的页面。
type notificationProvider struct {
	openedNotification
	delivered deliveredNotifications
	mu        sync.Mutex
	pending   map[string]chan notificationResult
	sequence  atomic.Uint64
}

// notificationLifecycle 在应用启动时订阅原生通知桥接的回报事件。
type notificationLifecycle struct {
	provider    *notificationProvider
	unsubscribe func()
}

// NewNotificationProvider 创建 Android 原生通知能力及其 Wails 生命周期服务。
func NewNotificationProvider() (Notifications, []application.Service) {
	provider := &notificationProvider{pending: make(map[string]chan notificationResult)}
	lifecycle := &notificationLifecycle{provider: provider}
	return provider, []application.Service{application.NewService(lifecycle)}
}

// ServiceStartup 订阅原生通知桥接的回报事件，并告知桥接可以转来通知点击；应用被点击通知唤起时，桥接暂存的点击随之转来。
func (l *notificationLifecycle) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	l.unsubscribe = application.Get().Event.On(notificationResultEvent, l.provider.settle)
	go func() {
		if _, err := l.provider.dispatch(context.Background(), map[string]any{"action": "listen-opened"}); err != nil {
			slog.Warn("登记移动端通知点击失败", "error", err)
		}
	}()
	return nil
}

// ServiceShutdown 取消订阅原生通知桥接的回报事件。
func (l *notificationLifecycle) ServiceShutdown() error {
	if l.unsubscribe != nil {
		l.unsubscribe()
		l.unsubscribe = nil
	}
	return nil
}

// settle 按请求编号把原生回报交给等待中的调用。
func (p *notificationProvider) settle(event *application.CustomEvent) {
	data, ok := event.Data.(map[string]any)
	if !ok {
		slog.Warn("移动端通知回报格式无法识别", "data", event.Data)
		return
	}
	if opened, _ := data["opened"].(bool); opened {
		path, _ := data["path"].(string)
		p.open(path)
		return
	}
	requestID, _ := data["requestId"].(string)
	if requestID == "" {
		return
	}
	succeeded, _ := data["ok"].(bool)
	permission, _ := data["permission"].(string)
	p.mu.Lock()
	waiter, found := p.pending[requestID]
	delete(p.pending, requestID)
	p.mu.Unlock()
	if !found {
		return
	}
	waiter <- notificationResult{
		ok:         succeeded,
		permission: appservice.NotificationPermissionStatus(permission),
	}
}

// dispatch 向原生通知桥接发起一次调用并等待回报。
func (p *notificationProvider) dispatch(ctx context.Context, payload map[string]any) (notificationResult, error) {
	requestID := strconv.FormatUint(p.sequence.Add(1), 10)
	payload["requestId"] = requestID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return notificationResult{}, err
	}

	waiter := make(chan notificationResult, 1)
	p.mu.Lock()
	p.pending[requestID] = waiter
	p.mu.Unlock()
	application.Android.Notify(string(encoded))

	timeout := time.NewTimer(notificationCallTimeout)
	defer timeout.Stop()
	select {
	case result := <-waiter:
		return result, nil
	case <-ctx.Done():
		p.discard(requestID)
		return notificationResult{}, ctx.Err()
	case <-timeout.C:
		p.discard(requestID)
		return notificationResult{}, errors.New("notification bridge timed out")
	}
}

// discard 丢弃已经不再等待回报的请求。
func (p *notificationProvider) discard(requestID string) {
	p.mu.Lock()
	delete(p.pending, requestID)
	p.mu.Unlock()
}

// permissionStatus 按原生回报返回授权状态，回报失败按不支持处理。
func permissionStatus(result notificationResult) appservice.NotificationPermissionStatus {
	switch result.permission {
	case appservice.NotificationPermissionStatusPrompt,
		appservice.NotificationPermissionStatusGranted,
		appservice.NotificationPermissionStatusDenied:
		return result.permission
	default:
		return appservice.NotificationPermissionStatusUnsupported
	}
}

// CheckNotificationPermission 检查当前设备的通知授权状态。
func (p *notificationProvider) CheckNotificationPermission(ctx context.Context, _ appservice.RequestMeta) (appservice.NotificationPermissionStatus, error) {
	result, err := p.dispatch(ctx, map[string]any{"action": "check-permission"})
	if err != nil {
		slog.Warn("检查移动端通知权限失败", "error", err)
		return appservice.NotificationPermissionStatusUnsupported, nil
	}
	return permissionStatus(result), nil
}

// RequestNotificationPermission 申请当前设备的通知授权。
func (p *notificationProvider) RequestNotificationPermission(ctx context.Context, _ appservice.RequestMeta) (appservice.NotificationPermissionStatus, error) {
	result, err := p.dispatch(ctx, map[string]any{"action": "request-permission"})
	if err != nil {
		slog.Warn("申请移动端通知权限失败", "error", err)
		return appservice.NotificationPermissionStatusUnsupported, nil
	}
	status := permissionStatus(result)
	slog.Info("移动端通知权限申请完成", "status", status)
	return status, nil
}

// SendMessageNotification 投递一条新消息通知，同一通知编号成功投递后在去重时长内不再投递。
func (p *notificationProvider) SendMessageNotification(ctx context.Context, _ appservice.RequestMeta, input appservice.MessageNotificationInput) error {
	return p.delivered.deliver(input.ID, func() error { return p.send(ctx, input) })
}

// send 经原生通知桥接投递一条新消息通知。
func (p *notificationProvider) send(ctx context.Context, input appservice.MessageNotificationInput) error {
	result, err := p.dispatch(ctx, map[string]any{
		"action": "notify",
		"id":     input.ID,
		"title":  input.Title,
		"body":   input.Body,
		"silent": !input.SoundEnabled,
		"path":   input.Path,
	})
	if err != nil {
		slog.Warn("投递移动端通知失败", "notification_id", input.ID, "error", err)
		return err
	}
	if !result.ok {
		slog.Warn("投递移动端通知失败", "notification_id", input.ID, "sound_enabled", input.SoundEnabled)
		return errors.New("post notification failed")
	}
	return nil
}
