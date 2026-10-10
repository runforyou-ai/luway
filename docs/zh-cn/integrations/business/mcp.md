---
title: MCP 接入
order: 2
---

业务系统可以通过 MCP 提供工具。

## 连接方式

支持 Streamable HTTP 与 SSE 两种连接方式，服务地址须能从服务端访问。认证方式附带的请求头见 [业务系统](/docs/zh-cn/integrations/business/connectors/)。AI 员工首次调用工具时才连接 MCP 服务，连接在本次运行内复用。

## 工具目录与性质

保存业务系统时读取 MCP 服务的工具列表。工具声明的 `readOnlyHint` 与 `destructiveHint` 用作默认性质：未声明只读的工具按写操作处理，未声明 `destructiveHint` 为 `false` 的写操作按不可撤销处理。性质与级别的说明见 [业务系统](/docs/zh-cn/integrations/business/connectors/)。

## 身份请求头

身份请求头在每次运行内固定，连接测试与工具更新只附带认证请求头，不附带身份请求头，此时 MCP 服务仍须返回工具列表。业务系统须按请求头中的身份返回数据，并校验订单等资源属于该身份。

## 测试与更新工具

列表页的 **测试连接** 检查已保存的连接，**更新工具** 重新读取全部业务系统的工具列表。更新后仍存在的工具保留其设置；更换服务地址时清除全部工具设置。
