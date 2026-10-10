//go:build ios
// iOS 本地通知权限与投递的原生入口。

#ifndef APP_NOTIFICATION_IOS_H
#define APP_NOTIFICATION_IOS_H

// 授权状态取值：0 未决定，1 已授权，2 已拒绝，-1 查询失败。
#define APP_NOTIFICATION_STATUS_PROMPT 0
#define APP_NOTIFICATION_STATUS_GRANTED 1
#define APP_NOTIFICATION_STATUS_DENIED 2
#define APP_NOTIFICATION_STATUS_UNKNOWN -1

// app_notification_authorization_status 返回当前应用的通知授权状态。
int app_notification_authorization_status(void);

// app_notification_request_authorization 申请通知授权并返回申请后的状态。
int app_notification_request_authorization(void);

// app_notification_post 按标识投递一条本地通知，silent 非零时不播放声音，path 非空时点击后打开该页面，返回 0 表示成功。
int app_notification_post(const char *identifier, const char *title, const char *body, int silent, const char *path);

// app_notification_listen 表示 Go 开始接收通知点击：此前暂存的点击随即转交 appNotificationOpened，之后的点击直接转交。
void app_notification_listen(void);

#endif
