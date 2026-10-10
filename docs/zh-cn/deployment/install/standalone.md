---
title: 独立程序
order: 2
---

在 Linux、Windows 或 macOS 上直接运行服务端程序。

## 下载

服务端需要 PostgreSQL，运行前先准备好连接信息。多台服务器运行时还需要开启 JetStream 的 NATS，见[多服务器运行](/docs/zh-cn/deployment/operate/multi-server/)。

从 Release 下载对应平台和架构的服务端压缩包并解压：Linux 与 macOS 为 `{{slug}}-server_<版本>_<系统>_<架构>.tar.gz`，Windows 为 `{{slug}}-server_<版本>_windows_<架构>.zip`。压缩包只含程序 `{{slug}}-server`（Windows 为 `{{slug}}-server.exe`），放在任意目录都可以运行。

## 写入启动配置

启动配置保存在固定位置的配置文件中，用 `config set` 逐项写入。配置文件不存在时新建，只允许当前用户读写：

```bash
./{{slug}}-server config set database.host 127.0.0.1
./{{slug}}-server config set database.port 5432
./{{slug}}-server config set database.user {{slug}}
./{{slug}}-server config set database.password <密码>
./{{slug}}-server config set database.name {{slug}}
./{{slug}}-server config set database.sslMode disable
```

`config path` 输出配置文件的位置，`config check` 校验配置，全部字段见[配置参考](/docs/zh-cn/deployment/configure/configuration/)。

配置文件与数据目录的位置按运行程序的系统用户确定：

| 系统 | 配置文件 | 数据目录 |
| --- | --- | --- |
| Linux | `~/.config/{{slug}}-server/server.yaml` | `~/.local/share/{{slug}}-server` |
| macOS | `~/Library/Application Support/{{slug}}-server/server.yaml` | `~/Library/Application Support/{{slug}}-server` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server\server.yaml` | `%LOCALAPPDATA%\{{slug}}-server` |

部署未开启对象存储时，本地文件保存在数据目录下的 `files`。数据要放到其他磁盘时，用 `data.directory` 指定绝对路径。

## 下载客户端文件

```bash
./{{slug}}-server download-clients
```

在服务端停止时执行。命令从 Release 下载与本程序同版本的发布清单与客户端文件，校验签名和校验值后放进程序所在目录，下载页由此提供桌面端安装包与执行器。服务器不能访问 Release 时，先在其他机器上下载同一 Release 中的 `release.json`、`release.json.sig` 与 `release.json` 列出的客户端文件，放进同一个目录，用 `--from <目录>` 指定该目录。不执行该命令时，下载页显示当前服务器未提供客户端安装包与执行器，见[客户端分发](/docs/zh-cn/deployment/operate/client-distribution/)。

## 运行

```bash
./{{slug}}-server run
```

服务端在前台运行，日志写到标准错误输出，按 `Ctrl+C` 停止。默认监听 `0.0.0.0:8080`，同一网络中的其他机器都能访问。

> [!WARNING]
> 启动后尽快完成首次安装，或先用防火墙限制访问，完成首次安装前任何能访问服务器的人都可以创建平台管理员。

完成首次安装和后续配置见[首次安装](/docs/zh-cn/deployment/configure/first-install/)。

程序不注册系统服务。需要开机自动启动或异常退出后重启时，用操作系统自带的方式托管 `{{slug}}-server run`，如 systemd、launchd 或 Windows 任务计划程序，并以写入配置时的同一系统用户运行。

## 命令

| 命令 | 说明 |
| --- | --- |
| `run` | 在前台运行服务端 |
| `status` | 显示部署版本，以及部署中各台服务器的版本和最近心跳 |
| `config path` | 输出配置文件的位置 |
| `config show` | 输出生效的配置，数据库密码以星号代替 |
| `config check` | 校验配置 |
| `config set <配置项> <值>` | 修改配置文件中的一项配置并保留文件中的注释，重启服务端后生效 |
| `download-clients` | 下载与本程序同版本的发布清单与客户端文件，在服务端停止时执行，`--from <目录>` 从本地目录读取 |
| `preflight` | 检查本程序能否以当前配置加入部署 |
| `public-url <部署地址>` | 修改部署地址，管理端无法访问时使用，见[HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/#无法访问管理端时修改部署地址) |
| `migrate status` | 显示数据库已执行与待执行的迁移数量 |
| `reset-server-id` | 为复制数据库搭建的新平台生成新的服务器标识 |
| `version` | 输出版本号与数据库迁移版本 |

升级与回退见[升级与回退](/docs/zh-cn/deployment/operate/upgrade/)。
