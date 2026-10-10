//go:build ios

#import <UIKit/UIKit.h>
#import <objc/runtime.h>
#import "server_link_ios.h"

// appServerLinkOpened 由 Go 导出，接收唤起应用的链接。
extern void appServerLinkOpened(char *url);

// 镜像加载时为 Wails 应用代理登记打开链接的处理；系统在 Go 启动前交来的链接先暂存，Go 开始接收后转交。

// 暂存的链接与 Go 是否开始接收，只在主线程读写。
static NSString *appPendingServerLink = nil;
static BOOL appServerLinkListenerReady = NO;

// 是否已为 Wails 应用代理登记打开链接的处理。
static BOOL appServerLinkHookInstalled = NO;

// appFlushServerLink 在 Go 已开始接收时转交暂存的链接，只在主线程调用。
static void appFlushServerLink(void) {
    if (!appServerLinkListenerReady || appPendingServerLink == nil) {
        return;
    }
    NSString *link = appPendingServerLink;
    appPendingServerLink = nil;
    appServerLinkOpened((char *)link.UTF8String);
}

// appOpenURL 暂存系统交来的链接并尝试转交 Go。
static BOOL appOpenURL(id delegate, SEL selector, UIApplication *application, NSURL *url, NSDictionary *options) {
    NSString *link = [url.absoluteString copy];
    dispatch_async(dispatch_get_main_queue(), ^{
        appPendingServerLink = link;
        appFlushServerLink();
    });
    return YES;
}

// AppServerLinkHook 在镜像加载时为 Wails 应用代理登记打开链接的处理，早于应用启动完成。
@interface AppServerLinkHook : NSObject
@end

@implementation AppServerLinkHook
+ (void)load {
    Class delegateClass = NSClassFromString(@"WailsAppDelegate");
    SEL selector = @selector(application:openURL:options:);
    appServerLinkHookInstalled = delegateClass != Nil && class_addMethod(delegateClass, selector, (IMP)appOpenURL, "B@:@@@");
}
@end

int app_server_link_listen(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        appServerLinkListenerReady = YES;
        appFlushServerLink();
    });
    return appServerLinkHookInstalled ? 1 : 0;
}
