---
title: 升级与回退
order: 2
---

升级服务端并同步提供对应版本的客户端。

## 升级前准备

- 升级前备份数据库，见[备份与恢复](/docs/zh-cn/deployment/operate/backup/)。
- 用 `{{slug}}-server version` 查看当前版本，在 Release 页面查看新版本。

## 升级独立程序

独立程序手动升级，见[独立程序](/docs/zh-cn/deployment/install/standalone/)：

1. 停止服务端。
2. 从 Release 下载目标版本的服务端压缩包，解压后替换原程序。
3. 执行 `{{slug}}-server download-clients`，下载与新程序同版本的发布清单与客户端文件；不能访问 Release 时用 `--from <目录>`。
4. 启动服务端。新版本加入部署时自动执行数据库迁移。

部署有多台服务器时，逐台替换程序并启动。第一台新版本服务器启动后，其余旧版本服务器自动退出，替换为新版本后再启动。

## 容器升级

换成新版本的镜像后重新创建容器。部署有多个容器时，先把每个容器换成新镜像，再逐个启动。

## 数据库迁移

服务端启动时自动执行数据库迁移，不需要手动操作。多台服务器同时启动时逐台加入部署，迁移只执行一次。

`{{slug}}-server migrate status` 显示数据库已执行与待执行的迁移数量。

## 回退

回退到较低版本时，要撤销较低版本没有的数据库迁移，这些迁移建立的数据随之删除。回退前先停止部署中的全部服务器：仍在运行的较高版本服务器会被进程管理器或容器自动重启，把部署版本改回较高版本。

独立程序在一台服务器上准备好较低版本的程序，依次执行：

```bash
./<较低版本目录>/{{slug}}-server version --json > lower.json
./<当前版本目录>/{{slug}}-server migrate down --to lower.json
./<较低版本目录>/{{slug}}-server preflight --accept-downgrade
```

然后用较低版本的程序替换各台服务器上的程序，执行 `download-clients` 后启动。

容器部署时，先停止全部容器，再用相同的配置文件依次执行：

```bash
docker run --rm --entrypoint /server <镜像>:<较低版本> version --json \
  | docker run -i --rm -v ./config:/etc/{{slug}}-server --entrypoint /server <镜像>:<当前版本> migrate down --to -
docker run --rm -v ./config:/etc/{{slug}}-server --entrypoint /server <镜像>:<较低版本> preflight --accept-downgrade
```

之后以较低版本的镜像启动容器。

`migrate down --to` 读取较低版本程序 `version --json` 的输出，撤销数据库中较低版本没有的迁移，`-` 表示从标准输入读取；部署中还有运行的服务器时命令停下，不修改数据库。`preflight --accept-downgrade` 把部署版本改为程序自己的版本，不加该参数时只检查不修改。

迁移撤销失败时，用升级前的数据库备份恢复，见[备份与恢复](/docs/zh-cn/deployment/operate/backup/)。

## 版本兼容

一个部署中的全部服务器运行同一版本。服务端启动时把版本登记到数据库：

- 低于部署版本的服务端拒绝启动，日志提示服务端版本低于部署版本。
- 高于部署版本的服务端改写部署版本，等其他版本的服务器全部退出后再执行数据库迁移并开始服务；异常退出的服务器在心跳超过 30 秒后不再等待。
- 正在运行的服务器在 10 秒内发现部署版本改变，停止服务并退出。
- 同一版本的服务端可以同时运行。

升级多台服务器时，第一台新版本服务器启动后旧版本服务器自动退出，到新版本完成迁移前服务短暂不可用，之后其余服务器以新版本启动。进程管理器与容器的重启策略会让旧版本服务器反复启动并拒绝启动，替换为新版本后恢复。
