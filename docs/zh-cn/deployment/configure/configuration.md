---
title: 配置参考
order: 1
---

服务端启动配置的全部字段。

服务端的配置分两层。启动配置写在每台服务器的配置文件中，包括连接部署所需的数据库与多台服务器运行时所需的 NATS，以及各服务器可以不同的监听地址、HTTPS 端口和出口 IP，修改后重启生效。部署地址与证书、部署名称、平台时区、文件存储、邮件发送和品牌由整个部署共用，保存在数据库中，平台管理员在「设置 → 平台 → 部署配置」修改，所有服务器在 10 秒内生效，见[部署地址与名称](/docs/zh-cn/deployment/configure/address/)、[文件存储](/docs/zh-cn/deployment/configure/storage/)、[邮件发送](/docs/zh-cn/deployment/configure/email/)和[品牌定制](/docs/zh-cn/deployment/configure/branding/)。

## 配置文件

服务端只从固定位置的配置文件读取启动配置，配置文件是 YAML 格式，不允许出现下表以外的字段，未写的字段使用默认值。配置文件的位置按运行程序的系统用户确定：

| 运行方式 | 配置文件 |
| --- | --- |
| Linux | `~/.config/{{slug}}-server/server.yaml` |
| macOS | `~/Library/Application Support/{{slug}}-server/server.yaml` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server\server.yaml` |
| 容器 | `/etc/{{slug}}-server/server.yaml`，部署时挂载配置目录 `/etc/{{slug}}-server` |

用 `{{slug}}-server config set <配置项> <值>` 修改配置文件中的一项配置，文件中的注释保留不变，配置文件不存在时新建并只允许当前用户读写；`config path` 输出配置文件的位置。见[独立程序](/docs/zh-cn/deployment/install/standalone/)与[容器部署](/docs/zh-cn/deployment/install/docker/)。

## 校验配置

- `{{slug}}-server config check` 校验配置，不连接数据库。
- `{{slug}}-server config show` 输出生效的配置，数据库密码以星号代替，NATS 地址去掉凭据。
- `{{slug}}-server preflight` 进一步连接数据库，检查本程序能否加入部署。

## 服务

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `server.host` | `0.0.0.0` | 服务监听地址；只允许本机访问时填 `127.0.0.1`，如由同一台机器上的反向代理转发 |
| `server.port` | `8080` | 服务监听端口，反向代理把请求转发到这里；直接提供 HTTPS 时须能经公网 80 端口访问 |
| `server.httpsPort` | 空 | 本服务器直接提供 HTTPS 的端口，如 `443`，使用部署地址的证书，与 `server.port` 提供同一套服务；为空表示由前面的代理提供 HTTPS，见[HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/) |
| `server.clientIPHeader` | 空 | 反向代理写入访问者真实 IP 的请求头，如 `CF-Connecting-IP`、`X-Real-IP`，用于登录、注册、网站访客发消息与上传、帮助中心搜索按 IP 限制频率；反向代理必须覆盖访问者自带的同名请求头，请求中没有该请求头或其值不是有效地址时取连接对端地址。为空时，直接提供 HTTPS 的服务器取 HTTPS 入口写入的访问者地址，其余取连接对端地址，见 [HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/#反向代理要求) |
| `server.countryHeader` | 空 | 反向代理写入访问者国家代码（两位字母，如 `CN`、`US`）的请求头，如 Cloudflare 的 `CF-IPCountry`，用于记录网站访客与新建工作区所在的地区；反向代理必须覆盖访问者自带的同名请求头。为空时不记录地区 |
| `server.egressIP` | 空 | 本服务器访问外部接口使用的公网出口 IP，显示在[微信开放平台](/docs/zh-cn/deployment/manage/wechat-open-platform/)页与[密钥接入公众号](/docs/zh-cn/integrations/channels/wechat-official-account-key/)渠道连接页的 IP 白名单中；多台服务器各自填写 |

## 数据库

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `database.host` | 空，必填 | PostgreSQL 地址 |
| `database.port` | 空，必填 | PostgreSQL 端口，通常为 `5432` |
| `database.user` | 空，必填 | 数据库账号 |
| `database.password` | 空，必填 | 数据库密码 |
| `database.name` | 空，必填 | 数据库名 |
| `database.sslMode` | 空，必填 | 连接加密方式：`disable`、`allow`、`prefer`、`require`、`verify-ca` 或 `verify-full` |

## NATS

后台任务与实时通知使用 NATS JetStream。未配置 `nats.url` 时，服务端启动内嵌 NATS，APP 账户承载任务与实时通知，CLIENT 账户放置成员、访客与电脑的连接，SYS 账户提供管理能力；服务凭据每次启动随机生成，签名种子保存在数据目录的 `nats/callout.seed`，应随整个 `nats` 目录备份。只监听本机随机 WebSocket 端口，由服务端 `/nats` 入口代理，各端仍使用同一个部署地址。这种模式只适用于单台服务器上的单个服务端进程。

服务器之间的消息总线保持独立：`nats.url` 为空时使用 PostgreSQL，配置后使用该 NATS。运行过程与成员通知共用账号级实时连接；运行私有频道按会话阅读资格授权，订阅后通过 HTTP 读取业务快照，跨实例快照经 NATS 分块传输。网站访客与电脑执行器同样经 jetcast 连接 `/nats`，通过公开访客接口或电脑接口换取最多五分钟的实时凭据。每次连接复核渠道、工作区、身份密钥或电脑凭据。电脑在线状态由独立 HTTP 心跳维护，消息通知不可用时仍每 30 秒领取任务。多台服务器须连接同一个外部 NATS，见[多服务器运行](/docs/zh-cn/deployment/operate/multi-server/)。

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `nats.url` | 空 | 外部 NATS 的 APP 服务账户地址，包含用户名和密码，多个地址用逗号分隔；如 `nats://app:密码@nats:4222` |
| `nats.systemURL` | 空 | 同一 NATS 的 SYS 系统账户地址，包含凭据，用于强制关闭被撤销的连接；外部模式必填 |
| `nats.calloutSeed` | 空 | `auth_callout.issuer` 对应的账户私钥种子；外部模式必填，全部应用节点使用同一个种子 |
| `nats.webSocketURL` | 空 | 服务端访问外部 NATS WebSocket 的 HTTP 或 HTTPS 内网地址，如 `http://nats:8222`；外部模式必填 |
| `nats.namespace` | `app` | 内部消息主题与任务队列前缀；实时前缀把 `_` 转义为 `__`、`-` 转义为 `_h` 后追加 `_realtime`，如 `app-prod` 对应 `app_hprod_realtime`。全部节点一致，以小写字母或数字开头，只含小写字母、数字、下划线和连字符 |
| `nats.replicas` | `1` | 任务队列、实时历史与连接登记的副本数，1 到 5；3 节点集群设为 `3`；内嵌模式只能为 1 |

外部 NATS 使用 2.15 或更新版本，开启持久化 JetStream、APP、CLIENT 和 SYS 账户、认证回调与 WebSocket。APP 服务用户和 SYS 用户列入 `auth_users`，`auth_callout.account` 保持为 APP，认证回调把客户端连接放入名为 `CLIENT` 的账户，账户名固定；CLIENT 账户只与 APP 交换实时前缀下的事件、控制与请求主题，限制每个连接的订阅数和客户端发出的请求大小，服务端发布的事件不受该限制。成员客户端提交业务登录令牌，访客与电脑提交短期实时凭据，由应用复核各自身份并授权精确私有频道；服务账户凭据只保存在服务端。当前每套 NATS APP 账户服务一个部署，所有节点使用相同的命名空间、签名种子和存储配置。

```text
listen: 0.0.0.0:4222
jetstream { store_dir: /var/lib/nats }
websocket { listen: 0.0.0.0:8222, no_tls: true }
accounts {
  APP {
    jetstream: enabled
    users: [{ user: app, password: $APP_PASSWORD }]
    exports: [
      { stream: "app_realtime.ev.>", accounts: [CLIENT] }
      { stream: "app_realtime.c.>", accounts: [CLIENT] }
    ]
    imports: [{ stream: { account: CLIENT, subject: "app_realtime.rq.>" } }]
  }
  CLIENT {
    limits: { max_subscriptions: 1000, max_payload: 65536 }
    exports: [{ stream: "app_realtime.rq.>", accounts: [APP] }]
    imports: [
      { stream: { account: APP, subject: "app_realtime.ev.>" } }
      { stream: { account: APP, subject: "app_realtime.c.>" } }
    ]
  }
  SYS { users: [{ user: sys, password: $SYS_PASSWORD }] }
}
system_account: SYS
authorization {
  timeout: 5s
  auth_callout { issuer: "<账户公钥>", account: APP, auth_users: [app, sys] }
}
```

示例中的 `app_realtime` 是默认命名空间对应的实时前缀，命名空间不同时替换为对应前缀。用 NATS `nsc generate nkey --account` 生成账户密钥，将私钥种子写入 `nats.calloutSeed`、对应公钥写入 `issuer`。WebSocket 端口仅向应用服务器和反向代理开放，公网使用 HTTPS/WSS。来源网站可能不同，Origin 不作为认证凭据；不要启用会拒绝这些来源的 `same_origin`。代理配置见[HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/)。

服务账户须能创建和读写任务 streams、`KV_<命名空间大写>_JOBS_STATE`、实时事件 stream 与连接登记 KV，并发布和订阅消息总线、任务、实时与运行快照主题（默认命名空间下为 `app.>`、`app-jobs.>`、`app_realtime.>` 与 `app_realtime_runs.>`，其他命名空间依次为 `<命名空间>.>`、`<命名空间>-jobs.>`、`<实时前缀>.>` 与 `<实时前缀>_runs.>`）、JetStream API、`$SYS.REQ.USER.AUTH` 和回复主题。系统账户须允许 `$SYS.REQ.SERVER.*.KICK` 请求及其回复。两个服务连接均须允许 `$SYS.REQ.USER.INFO`，启动时据此核对 APP 与 SYS 账户。配置展示隐藏签名种子和连接凭据。

数据目录中存在默认 `$G` 账户的旧任务数据时，服务端拒绝启动 APP 账户，旧数据保持原样；升级前须先处理原队列并归档旧账户数据。

成员、访客与电脑连接可恢复短时断线期间的事件；历史过期后客户端重新读取业务数据。输入状态不保存在实时历史中。切换到外部 NATS 或更换 APP 账户前应先处理完原队列；旧账户的任务与实时历史不会自动迁移。

成员失权时强制关闭原连接，并通过持久任务补偿临时的 SYS 管理请求故障。各连接的补偿登记具有独立时限，补偿只针对原 NATS 连接，其他工作区的合法重连可继续使用。独立内嵌 NATS 重启后，已退出实例的连接撤销视为完成；外部 NATS 仍经 SYS 确认强制关闭。故障期间应恢复系统账户连接及 KICK 权限，并检查服务端撤销错误和维护队列；成员连接凭据最长二十分钟有效，客户端在到期前换用新连接并重新认证，补偿持续到关闭成功或有效期上限。NATS 与应用服务器应保持时钟同步。恢复业务快照遇到临时 HTTP 故障时，客户端会继续重试。

## 日志

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `log.level` | `info` | 本地日志输出的最低级别：`debug`、`info`、`warn` 或 `error`；写入数据库的日志始终包含全部级别，见[监控与诊断](/docs/zh-cn/deployment/operate/monitoring/#日志) |

## 数据目录

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `data.directory` | 平台数据目录 | 本服务器的数据目录，须为绝对路径；部署未开启对象存储时，文件保存在其下的 `files`；未配置 NATS 时，后台任务保存在其下的 `nats` |

不填 `data.directory` 时使用平台数据目录：

| 运行方式 | 数据目录 |
| --- | --- |
| Linux | `~/.local/share/{{slug}}-server` |
| macOS | `~/Library/Application Support/{{slug}}-server` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server` |
| 容器 | `/var/lib/{{slug}}-server`，挂载卷以持久保存 |

发布清单与客户端安装包、更新包和执行器固定放在服务端程序所在目录中，不需要配置，见[客户端分发](/docs/zh-cn/deployment/operate/client-distribution/)。
