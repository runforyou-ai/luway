//go:build ios
// iOS 应用图标角标的原生入口。

#ifndef APP_UNREAD_INDICATOR_IOS_H
#define APP_UNREAD_INDICATOR_IOS_H

// app_unread_set_badge 设置应用图标角标数量，count 不大于 0 时清除角标。
void app_unread_set_badge(int count);

#endif
