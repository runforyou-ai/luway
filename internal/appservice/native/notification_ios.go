//go:build !server && ios

package native

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework UserNotifications
#include <stdlib.h>
#include "notification_ios.h"
*/
import "C"

import (
	"context"
	"errors"
	"log/slog"
	"unsafe"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// notificationProvider 通过系统通知中心提供 iOS 本地通知能力，点击通知后打开通知携带的页面。
type notificationProvider struct {
	openedNotification
	delivered deliveredNotifications
}

// iosNotifications 是接收通知点击的唯一通知能力实例。
var iosNotifications = &notificationProvider{}

// NewNotificationProvider 创建 iOS 原生通知能力，并开始接收通知点击；应用被点击通知唤起时暂存的点击随即转来。
func NewNotificationProvider() (Notifications, []application.Service) {
	C.app_notification_listen()
	return iosNotifications, nil
}

// appNotificationOpened 接收原生层转来的被点击通知的页面地址。
//
//export appNotificationOpened
func appNotificationOpened(path *C.char) {
	iosNotifications.open(C.GoString(path))
}

// notificationPermissionStatus 把原生授权取值转换为应用服务的授权状态。
func notificationPermissionStatus(status C.int) appservice.NotificationPermissionStatus {
	switch status {
	case C.APP_NOTIFICATION_STATUS_PROMPT:
		return appservice.NotificationPermissionStatusPrompt
	case C.APP_NOTIFICATION_STATUS_GRANTED:
		return appservice.NotificationPermissionStatusGranted
	case C.APP_NOTIFICATION_STATUS_DENIED:
		return appservice.NotificationPermissionStatusDenied
	default:
		return appservice.NotificationPermissionStatusUnsupported
	}
}

// CheckNotificationPermission 检查当前设备的通知授权状态。
func (*notificationProvider) CheckNotificationPermission(_ context.Context, _ appservice.RequestMeta) (appservice.NotificationPermissionStatus, error) {
	return notificationPermissionStatus(C.app_notification_authorization_status()), nil
}

// RequestNotificationPermission 申请当前设备的通知授权。
func (*notificationProvider) RequestNotificationPermission(_ context.Context, _ appservice.RequestMeta) (appservice.NotificationPermissionStatus, error) {
	status := notificationPermissionStatus(C.app_notification_request_authorization())
	slog.Info("移动端通知权限申请完成", "status", status)
	return status, nil
}

// SendMessageNotification 投递一条新消息通知，同一通知编号成功投递后在去重时长内不再投递。
func (p *notificationProvider) SendMessageNotification(_ context.Context, _ appservice.RequestMeta, input appservice.MessageNotificationInput) error {
	return p.delivered.deliver(input.ID, func() error { return postNotification(input) })
}

// postNotification 向系统通知中心投递一条新消息通知。
func postNotification(input appservice.MessageNotificationInput) error {
	identifier := C.CString(input.ID)
	defer C.free(unsafe.Pointer(identifier))
	title := C.CString(input.Title)
	defer C.free(unsafe.Pointer(title))
	body := C.CString(input.Body)
	defer C.free(unsafe.Pointer(body))
	path := C.CString(input.Path)
	defer C.free(unsafe.Pointer(path))
	// 通知声音由本机偏好控制，关闭时投递静音通知。
	silent := C.int(1)
	if input.SoundEnabled {
		silent = C.int(0)
	}
	if C.app_notification_post(identifier, title, body, silent, path) != 0 {
		slog.Warn("投递移动端通知失败", "notification_id", input.ID, "sound_enabled", input.SoundEnabled)
		return errors.New("post notification failed")
	}
	return nil
}
