//go:build ios

#import <UIKit/UIKit.h>
#import <UserNotifications/UserNotifications.h>
#import "unread_indicator_ios.h"

void app_unread_set_badge(int count) {
    NSInteger badge = count > 0 ? count : 0;
    if (badge == 0) {
        // 未读归零和退出登录时撤回通知中心里已投递的消息通知。
        [[UNUserNotificationCenter currentNotificationCenter] removeAllDeliveredNotifications];
    }
    dispatch_async(dispatch_get_main_queue(), ^{
        if (@available(iOS 16.0, *)) {
            [[UNUserNotificationCenter currentNotificationCenter] setBadgeCount:badge
                                                         withCompletionHandler:nil];
            return;
        }
        [UIApplication sharedApplication].applicationIconBadgeNumber = badge;
    });
}
