/** 消息渠道及类型扩展调用。 */
import { isApiError } from "@/api/client"
import {
  ChannelType,
  type TelegramChannel,
  type WebsiteChannel,
  type WechatAuthorizationChannel,
  type WechatKeyChannel,
  type WeComBotChannel,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

/** 连接公众号或更新密钥接入凭据，凭据通过微信验证后才保存。 */
export const saveWechatKeyChannelConnection = ops.saveWechatKeyChannelConnection

/** 立即重新获取密钥接入公众号的接口调用凭据。 */
export const checkWechatKeyChannelConnection = ops.checkWechatKeyChannelConnection

/** 发起公众号授权，返回在浏览器中打开的授权发起页地址。 */
export const startWechatAuthorization = ops.startWechatAuthorization

/** 立即重新获取授权接入公众号的接口调用凭据。 */
export const checkWechatAuthorizationChannelConnection = ops.checkWechatAuthorizationChannelConnection

/** 创建消息渠道。 */
export const createMessageChannel = ops.createMessageChannel

/** 修改消息渠道基础信息。 */
export const updateMessageChannel = ops.updateMessageChannel

/** 修改消息渠道接待设置。 */
export const updateMessageChannelReception = ops.updateMessageChannelReception

/** 测试 Telegram 草稿 Token。 */
export const testTelegramChannelConnection = ops.testTelegramChannelConnection

/** 保存 Telegram 接入方式、机器人和回调设置。 */
export const saveTelegramChannelConnection = ops.saveTelegramChannelConnection

/** 重新生成业务系统转发 Telegram 消息使用的转发密钥。 */
export const regenerateTelegramGatewaySecret = ops.regenerateTelegramGatewaySecret

/** 判断保存是否需要用户确认复用其他渠道的 Telegram Bot。 */
export function isTelegramBotReuseConfirmationError(error: unknown) {
  return (
    isApiError(error) &&
    error.kind === "conflict" &&
    error.reason === "telegram_bot_reuse_confirmation_required"
  )
}

/** 保存企业微信智能机器人的长连接凭据。 */
export const saveWeComBotChannelConnection = ops.saveWeComBotChannelConnection

/** 判断保存失败是否因为机器人已被其他渠道使用。 */
export function isWeComBotInUseError(error: unknown) {
  return (
    isApiError(error) &&
    error.kind === "conflict" &&
    error.reason === "wecom_bot_in_use"
  )
}

/** 读取服务员工渠道中的外部账号及其绑定成员。 */
export function listChannelAccounts(channelID: string, signal?: AbortSignal) {
  return ops.listChannelAccounts(channelID, signal).then(
    (list) => list.accounts,
  )
}

/** 把外部账号绑定或改绑到成员。 */
export const bindChannelAccount = ops.bindChannelAccount

/** 解除外部账号与成员的绑定。 */
export const unbindChannelAccount = ops.unbindChannelAccount

/** 读取绑定链接对应的工作区、渠道与外部账号。 */
export const previewChannelBinding = ops.previewChannelBinding

/** 把绑定链接对应的外部账号绑定到当前账号。 */
export const confirmChannelBinding = ops.confirmChannelBinding

/** 修改网站渠道聊天界面。 */
export const updateWebsiteChannelChatInterface = ops.updateWebsiteChannelChatInterface

/** 修改网站渠道允许使用的网站。 */
export const updateWebsiteChannelAccess = ops.updateWebsiteChannelAccess

/** 修改网站渠道帮助中心发布的知识库。 */
export const updateWebsiteChannelHelpCenter = ops.updateWebsiteChannelHelpCenter

/** 修改网站渠道聊天窗口首页。 */
export const updateWebsiteChannelHome = ops.updateWebsiteChannelHome

/** 停用消息渠道。 */
export const deactivateMessageChannel = ops.deactivateMessageChannel

/** 启用消息渠道。 */
export const activateMessageChannel = ops.activateMessageChannel

/** 读取当前企业的渠道选择项。 */
export function listChannelOptions(signal?: AbortSignal) {
  return ops.listChannelOptions(signal).then((list) => list.channels)
}

/** 读取消息渠道列表。 */
export function listMessageChannels() {
  return ops.listMessageChannels().then((list) => list.channels)
}

/** 读取当前支持的消息渠道类型及其能力。 */
export function listMessageChannelTypes() {
  return ops.listMessageChannelTypes().then((list) => list.types)
}

/** 各渠道类型的详情结构。 */
type MessageChannelDetailMap = {
  [ChannelType.Website]: WebsiteChannel
  [ChannelType.Telegram]: TelegramChannel
  [ChannelType.WechatKey]: WechatKeyChannel
  [ChannelType.WechatAuthorization]: WechatAuthorizationChannel
  [ChannelType.WeComBot]: WeComBotChannel
}

/** 按服务端返回的渠道类型区分的渠道详情，T 限定为部分渠道类型时只含对应详情。 */
export type MessageChannelDetail<T extends ChannelType = ChannelType> = {
  [K in T]: MessageChannelDetailMap[K] & { type: K }
}[T]

/** 以服务端返回的渠道类型标注详情，类型与读取入口不一致时报错。 */
function typedChannel<T extends ChannelType, C extends { type: ChannelType }>(type: T, channel: C): C & { type: T } {
  if (channel.type !== type) throw new Error(`渠道类型 ${channel.type} 与读取入口 ${type} 不一致`)
  return { ...channel, type }
}

/** 按渠道类型读取渠道详情。 */
export async function getMessageChannelDetail(
  type: ChannelType,
  id: string,
  signal?: AbortSignal,
): Promise<MessageChannelDetail> {
  switch (type) {
    case ChannelType.Website:
      return typedChannel(type, await ops.getWebsiteChannel(id, signal))
    case ChannelType.Telegram:
      return typedChannel(type, await ops.getTelegramChannel(id, signal))
    case ChannelType.WechatKey:
      return typedChannel(type, await ops.getWechatKeyChannel(id, signal))
    case ChannelType.WechatAuthorization:
      return typedChannel(type, await ops.getWechatAuthorizationChannel(id, signal))
    case ChannelType.WeComBot:
      return typedChannel(type, await ops.getWeComBotChannel(id, signal))
  }
}
