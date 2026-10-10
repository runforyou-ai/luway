package domain

// NotificationView 定义点击用户通知后打开的视图：服务会话在客服收件箱打开，其余会话按类型在消息中打开，待处理操作在待处理列表打开。
type NotificationView string

const (
	NotificationViewService NotificationView = "service"
	NotificationViewAgent   NotificationView = "agent"
	NotificationViewDirect  NotificationView = "direct"
	NotificationViewGroup   NotificationView = "group"
	NotificationViewPending NotificationView = "pending"
)

// PushPlatform 定义离线推送的设备平台。
type PushPlatform string

const (
	PushPlatformAndroid PushPlatform = "android"
	PushPlatformIOS     PushPlatform = "ios"
)

// Valid 判断推送平台是否受支持。
func (p PushPlatform) Valid() bool {
	return p == PushPlatformAndroid || p == PushPlatformIOS
}
