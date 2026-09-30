//go:build ios

#import <UserNotifications/UserNotifications.h>
#import <objc/runtime.h>
#import "notification_ios.h"

// appNotificationOpened 由 Go 导出，接收被点击的通知携带的页面地址。
extern void appNotificationOpened(char *path);

// 通知附加数据中页面地址的键。
static NSString *const appNotificationPathKey = @"path";

// 查询状态和投递通知的最长等待时间。
static const int64_t appNotificationTimeoutNanos = 10 * NSEC_PER_SEC;

// 申请授权的最长等待时间，系统弹窗要等用户操作。
static const int64_t appNotificationAuthorizationTimeoutNanos = 2 * 60 * NSEC_PER_SEC;

// appNotificationStatus 把系统授权状态转换为对外状态取值。
static int appNotificationStatus(UNAuthorizationStatus status) {
    switch (status) {
        case UNAuthorizationStatusNotDetermined:
            return APP_NOTIFICATION_STATUS_PROMPT;
        case UNAuthorizationStatusDenied:
            return APP_NOTIFICATION_STATUS_DENIED;
        case UNAuthorizationStatusAuthorized:
        case UNAuthorizationStatusProvisional:
        case UNAuthorizationStatusEphemeral:
            return APP_NOTIFICATION_STATUS_GRANTED;
        default:
            return APP_NOTIFICATION_STATUS_UNKNOWN;
    }
}

// appNotificationString 把 C 字符串转换为非空 NSString。
static NSString *appNotificationString(const char *value) {
    if (value == NULL) {
        return @"";
    }
    NSString *converted = [NSString stringWithUTF8String:value];
    return converted != nil ? converted : @"";
}

int app_notification_authorization_status(void) {
    __block int status = APP_NOTIFICATION_STATUS_UNKNOWN;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    [[UNUserNotificationCenter currentNotificationCenter]
        getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
            status = appNotificationStatus(settings.authorizationStatus);
            dispatch_semaphore_signal(done);
        }];
    if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, appNotificationTimeoutNanos)) != 0) {
        return APP_NOTIFICATION_STATUS_UNKNOWN;
    }
    return status;
}

int app_notification_request_authorization(void) {
    __block int status = APP_NOTIFICATION_STATUS_UNKNOWN;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    UNAuthorizationOptions options =
        UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge;
    [[UNUserNotificationCenter currentNotificationCenter]
        requestAuthorizationWithOptions:options
                      completionHandler:^(BOOL granted, NSError *error) {
                          if (error != nil) {
                              status = APP_NOTIFICATION_STATUS_UNKNOWN;
                          } else {
                              status = granted ? APP_NOTIFICATION_STATUS_GRANTED
                                               : APP_NOTIFICATION_STATUS_DENIED;
                          }
                          dispatch_semaphore_signal(done);
                      }];
    if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, appNotificationAuthorizationTimeoutNanos)) != 0) {
        // 等待超时后按系统记录的授权状态返回，避免把用户已完成的授权报成失败。
        return app_notification_authorization_status();
    }
    return status;
}

int app_notification_post(const char *identifier, const char *title, const char *body, int silent, const char *path) {
    UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
    content.title = appNotificationString(title);
    content.body = appNotificationString(body);
    NSString *openPath = appNotificationString(path);
    if (openPath.length > 0) {
        content.userInfo = @{appNotificationPathKey: openPath};
    }
    if (silent == 0) {
        content.sound = [UNNotificationSound defaultSound];
    }
    NSString *requestID = appNotificationString(identifier);
    if (requestID.length == 0) {
        requestID = [[NSUUID UUID] UUIDString];
    }
    // trigger 为空表示立即投递，相同标识的通知在通知中心中替换前一条。
    UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:requestID
                                                                         content:content
                                                                         trigger:nil];
    __block int result = 0;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    [[UNUserNotificationCenter currentNotificationCenter]
        addNotificationRequest:request
         withCompletionHandler:^(NSError *error) {
             result = error != nil ? -1 : 0;
             dispatch_semaphore_signal(done);
         }];
    if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, appNotificationTimeoutNanos)) != 0) {
        return -1;
    }
    return result;
}

// Wails 在应用启动完成前登记自己的通知中心代理（负责前台展示），但点击时不转交通知内容。
// 这里在镜像加载时包装该代理的点击处理：只读取「打开」动作携带的页面地址，其余照常交给 Wails。
// 应用被点击通知唤起时，系统在 Go 启动前就交来点击，页面地址先暂存在这里，等 Go 开始接收再转交。

// 暂存的页面地址与 Go 是否开始接收，只在主线程读写。
static NSString *appPendingOpenedPath = nil;
static BOOL appOpenedListenerReady = NO;

// Wails 代理原有的点击处理。
static IMP appOriginalDidReceive = NULL;

// appFlushOpenedNotification 在 Go 已开始接收时转交暂存的页面地址，只在主线程调用。
static void appFlushOpenedNotification(void) {
    if (!appOpenedListenerReady || appPendingOpenedPath == nil) {
        return;
    }
    NSString *path = appPendingOpenedPath;
    appPendingOpenedPath = nil;
    appNotificationOpened((char *)path.UTF8String);
}

// appDidReceiveNotificationResponse 读取被打开的通知携带的页面地址后调用 Wails 原有的点击处理；划掉或关闭通知不打开页面。
static void appDidReceiveNotificationResponse(id delegate, SEL selector, UNUserNotificationCenter *center,
                                                UNNotificationResponse *response, void (^completionHandler)(void)) {
    if ([response.actionIdentifier isEqualToString:UNNotificationDefaultActionIdentifier]) {
        id path = response.notification.request.content.userInfo[appNotificationPathKey];
        NSString *openPath = [path isKindOfClass:[NSString class]] ? [path copy] : @"";
        dispatch_async(dispatch_get_main_queue(), ^{
            appPendingOpenedPath = openPath;
            appFlushOpenedNotification();
        });
    }
    ((void (*)(id, SEL, UNUserNotificationCenter *, UNNotificationResponse *, void (^)(void)))appOriginalDidReceive)(
        delegate, selector, center, response, completionHandler);
}

// AppNotificationHook 在镜像加载时包装 Wails 通知中心代理的点击处理，早于应用启动完成。
@interface AppNotificationHook : NSObject
@end

@implementation AppNotificationHook
+ (void)load {
    Class delegateClass = NSClassFromString(@"MFNotificationDelegate");
    SEL selector = @selector(userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:);
    Method method = delegateClass != Nil ? class_getInstanceMethod(delegateClass, selector) : NULL;
    if (method == NULL) {
        NSLog(@"Notification: Wails notification delegate not found, notification taps only bring the app to the front");
        return;
    }
    appOriginalDidReceive = method_setImplementation(method, (IMP)appDidReceiveNotificationResponse);
}
@end

void app_notification_listen(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        appOpenedListenerReady = YES;
        appFlushOpenedNotification();
    });
}
