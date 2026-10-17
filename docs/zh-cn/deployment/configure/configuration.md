---
title: 配置参考
order: 1
---

服务端启动配置的全部字段。

服务端的配置分两层。启动配置写在每台服务器的配置文件中，包括连接部署所需的数据库与多台服务器运行时所需的 NATS，以及各服务器可以不同的监听地址、HTTPS 端口，修改后重启生效。访问地址与证书、部署名称、平台时区、文件存储、邮件发送和品牌由整个部署共用，保存在数据库中，平台管理员在「平台后台 → 系统设置」修改，所有服务器在 10 秒内生效，见[访问地址与名称](/docs/zh-cn/deployment/configure/address/)、[文件存储](/docs/zh-cn/deployment/configure/storage/)、[邮件发送](/docs/zh-cn/deployment/configure/email/)和[品牌定制](/docs/zh-cn/deployment/configure/branding/)。

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
| `server.httpsPort` | 空 | 本服务器直接提供 HTTPS 的端口，如 `443`，使用访问地址的证书，与 `server.port` 提供同一套服务；为空表示由前面的代理提供 HTTPS，见[HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/) |
| `server.clientIPHeader` | 空 | 反向代理写入访问者真实 IP 的请求头，如 `CF-Connecting-IP`、`X-Real-IP`，用于登录、注册、网站访客发消息与上传、帮助中心搜索按 IP 限制频率；反向代理必须覆盖访问者自带的同名请求头，请求中没有该请求头或其值不是有效地址时取连接对端地址。为空时，直接提供 HTTPS 的服务器取 HTTPS 入口写入的访问者地址，其余取连接对端地址，见 [HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/#反向代理要求) |
| `server.countryHeader` | 空 | 反向代理写入访问者国家代码（两位字母，如 `CN`、`US`）的请求头，如 Cloudflare 的 `CF-IPCountry`，用于记录网站访客与新建工作区所在的地区；反向代理必须覆盖访问者自带的同名请求头。为空时不记录地区 |

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

后台任务、实时事件历史和实例间通信共用内部 NATS。`nats.url` 留空时，应用在数据目录的 `nats` 子目录启动持久化内嵌 NATS，无需额外服务或网络监听。内嵌模式适用于单个应用进程，备份时保存整个数据目录。

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `nats.url` | 空 | 可选外部 NATS 连接地址，支持 `nats://` 和 `tls://`，凭据写在 URL 中；多个引导地址用逗号分隔，如 `nats://app:密码@nats:4222` |

应用配置无需区分外部单节点或集群。可以使用统一的服务地址，NATS 客户端也会发现集群公布的节点地址；这些地址必须能被应用服务器访问。外部连接失败时应用启动失败。每个部署使用独立的 NATS 账户，账户名称由部署者决定。

外部 NATS 使用 2.15 或更新版本并开启 JetStream。下面是独立单节点的最小配置，NATS 仅向应用服务器开放：

```text
listen: 0.0.0.0:4222
jetstream { store_dir: /var/lib/nats }
authorization { users: [{ user: app, password: $APP_PASSWORD }] }
```

配置 `nats.url` 后，在启动应用前执行一次资源初始化：

```sh
luway-server nats-init --replicas=1 --storage=file
```

三节点集群由部署者配置路由、持久存储和认证，并在初始化时使用 `--replicas=3`。副本与存储参数属于资源管理命令。应用启动仅检查资源契约，保留已有副本、容量和放置策略。已有资源的副本或存储类型与命令参数不一致时，初始化报错，部署者通过 NATS 管理工具显式调整。

初始化创建 `APP_JOBS`、`APP_JOBS_DEAD`、`KV_APP_JOBS_STATE` 与 `APP_REALTIME`。运行账户需要任务消费者管理、JetStream 发布与读取、KV 读写，以及 `app.>`、`app-jobs.>`、JetStream API 和回复主题的访问权；初始化还需要创建及更新资源的权限。后台任务、权限撤销、会话失权与用户通知在业务数据库事务内登记到 outbox，发布确认后删除，失败会重试；会话变更、输入状态、电脑唤醒与报表刷新等瞬时提示只在提交后发送，不保证进程崩溃时补发。任务处理器保持业务幂等。投递采用至少一次语义：任务流的去重窗口为两小时，实时流为五分钟；超过窗口的重试可能再次投递，客户端仅在当前频道的有界缓存内按事件标识去重。

成员、网站访客与电脑连接应用的 `/realtime` WebSocket 网关，前端从 HTTP 接口取得地址与短期票据。成员票据最多两分钟，访客和电脑票据最多五分钟；长期登录或电脑凭据只用于 HTTP。网关复核身份与精确频道权限，权限变更经持久事件通知各节点关闭旧连接，定期复核用于补充故障恢复。NATS 断开时网关关闭客户端连接，客户端获取新票据重连。

持久实时事件保留五分钟，断线后通过游标恢复；历史过期、资源重建或恢复超过 1000 帧或 4 MiB 时，服务端明确报告恢复失败并推进到新游标，客户端重新读取 HTTP 业务快照；瞬时提示不保证补放。输入状态即时发送。运行流保留业务序号与跨实例分块快照。电脑仍使用独立 HTTP 心跳和 30 秒任务领取轮询。代理将 `/realtime` 转发到应用，见[HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/)。

多个应用进程必须连接同一外部 NATS。内嵌模式通过数据库独占锁限制为一个进程，同一主机上的多个进程也受此限制。切换到外部 NATS 或另一个账户前，先处理原队列并停止应用；原 NATS 中的任务和历史不会自动迁移。见[多服务器运行](/docs/zh-cn/deployment/operate/multi-server/)。

## 日志

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `log.level` | `info` | 本地日志输出的最低级别：`debug`、`info`、`warn` 或 `error`；控制台与按天轮转的文件使用同一日志级别，见[监控与诊断](/docs/zh-cn/deployment/operate/monitoring/#日志) |

## 数据目录

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `data.directory` | 平台数据目录 | 本服务器的数据目录，须为绝对路径；未配置 NATS 时，后台任务保存在其下的 `nats` |

不填 `data.directory` 时使用平台数据目录：

| 运行方式 | 数据目录 |
| --- | --- |
| Linux | `~/.local/share/{{slug}}-server` |
| macOS | `~/Library/Application Support/{{slug}}-server` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server` |
| 容器 | `/var/lib/{{slug}}-server`，挂载卷以持久保存 |

发布清单与客户端安装包、更新包和执行器固定放在服务端程序所在目录中，不需要配置，见[客户端分发](/docs/zh-cn/deployment/operate/client-distribution/)。
