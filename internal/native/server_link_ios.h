//go:build ios
// iOS 唤起链接的原生入口。

#ifndef APP_SERVER_LINK_IOS_H
#define APP_SERVER_LINK_IOS_H

// app_server_link_listen 表示 Go 开始接收唤起链接：此前暂存的链接随即转交 appServerLinkOpened，之后的链接直接转交；返回 0 表示未能登记打开链接的处理。
int app_server_link_listen(void);

#endif
