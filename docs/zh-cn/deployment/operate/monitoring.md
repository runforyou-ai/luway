---
title: 监控与诊断
order: 6
---

检查服务状态，查看日志并处理诊断信息。

## 健康检查

服务端提供两个健康检查地址，都支持 `GET` 和 `HEAD`。`GET` 返回 JSON，`HEAD` 只返回相同的状态码和响应头：

| 地址 | 用途 |
| --- | --- |
| `/healthz` | 进程存活检查。服务端在运行就返回 `200` 和 `{"status":"ok"}`，不检查依赖，适合作为容器或进程管理器的存活探针 |
| `/readyz` | 就绪检查。服务端检查消息总线连接，并在 3 秒内查询一次 PostgreSQL，都正常时返回 `200` 和 `{"status":"ready"}`，否则返回 `503` 和 `{"status":"unavailable"}`，适合作为负载均衡摘除节点的依据 |


## 日志

服务端以 JSON 格式同时输出到标准输出和数据目录中的 `logs/server-YYYY-MM-DD-<进程编号>.jsonl`。文件按 UTC 日期轮转，保留最近七天；最低日志级别由 `log.level` 设置，默认 `info`。运维可以用 Alloy 采集文件或容器标准输出，按 `trace_id` 关联 HTTP 请求和后续任务。HTTP 请求日志中的 `duration_ms` 表示耗时。

## 客户端与执行器日志

- 桌面端把日志写到标准错误和本机文件 `logs/desktop.log`，内置执行器的日志一并写入。文件位于桌面端数据目录：macOS 为 `~/Library/Application Support/{{slug}}/logs/desktop.log`，Windows 为 `%LOCALAPPDATA%\{{slug}}\logs\desktop.log`，Linux 为 `~/.local/share/{{slug}}/logs/desktop.log`。
- 无界面执行器写入数据目录下的 `logs/executor.log`，数据目录见[工作区电脑](/docs/zh-cn/integrations/computers/#启动执行器)。
- 日志文件超过 10 MB 时改名为 `.1` 并新建文件，只保留一个旧文件。移动端不写日志文件。

执行器执行派发给这台电脑的操作时，日志带有派发时的串联编号 `trace_id`，同步共享文件、上报结果等请求也带上这个编号，与服务端日志中同一编号的记录对应。


## 诊断导出

平台管理员可下载包含运行摘要和本机日志的 ZIP，或主动上传到支持服务。操作见[运行状态与诊断](/docs/zh-cn/deployment/manage/diagnostics/)。

## 官方支持服务

首次安装完成后，服务端向官方支持服务登记部署身份。管理员在线激活时用激活码换取授权码；主动提交诊断包时上传日志到支持服务。移动端推送中继尚未提供，相关请求不会成功投递。
