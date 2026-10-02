---
title: Telegram
order: 4
---

通过 Telegram 机器人接待客户。

本页内容正在编写。

## 创建机器人

## 连接机器人

## Webhook

## 账号绑定与身份识别

业务系统已有自己的 Telegram 机器人（例如用于账号绑定）时，可以把渠道的接入方式设为「业务系统转发」：机器人的 Webhook 仍由业务系统接收，业务系统把私聊消息转发给{{product}}，{{product}}用同一个 Bot Token 回复客户。

{{product}}在业务系统转发方式下不登记也不删除机器人的 Webhook。从直接连接改为业务系统转发后，业务系统需要调用 Telegram 的 `setWebhook` 把 Webhook 设为自己的地址，替换{{product}}此前登记的地址。

转发请求：

- 地址与密钥取渠道连接信息中的「转发地址」和「转发密钥」。
- 请求体原样使用 Telegram 推送的 Update JSON，请求头 `X-Telegram-Bot-Api-Secret-Token` 填写转发密钥。
- 客户已在业务系统中绑定 Telegram 账号时，在请求头 `X-Customer-Token` 附带客户签名身份，签发方式与网站挂件的客户签名身份相同（设置中的客户身份密钥，HS256，`sub` 为业务系统用户编号）。附带签名身份的客户视为已验证，AI 员工可以按客户查询业务数据。
- 未附带签名身份的消息按未验证客户处理；同一 Telegram 用户此前的验证随之取消。
- 同一 Update 重复转发只记录一次，重复转发不改变客户当前的验证状态。

```http
POST <转发地址>
Content-Type: application/json
X-Telegram-Bot-Api-Secret-Token: <转发密钥>
X-Customer-Token: <客户签名身份>

{"update_id": 1001, "message": { ... }}
```

| 响应状态 | 含义 |
| --- | --- |
| 204 | 已接收 |
| 401 | 转发密钥不正确 |
| 403 | 客户签名身份无效或已过期，消息未接收 |
| 404 | 渠道不存在或已停用 |
| 503 | 暂时无法处理，稍后重试 |

客户首次以某个业务用户身份发来消息时，该 Telegram 账号的会话会归入这位用户已有的客户资料（例如此前通过网站登录咨询过）。业务系统必须确认 Telegram 账号确实属于该用户后才能附带签名身份，例如通过一次性绑定链接完成绑定。
