/** 按渠道类型登记编辑页定义。 */
import { ChannelType } from "@/api"
import type { ChannelEditDefinition, ChannelEditDefinitions } from "@/features/channels/channel-edit-definition"
import { telegramChannelEdit } from "@/features/channels/telegram/telegram-channel-edit"
import { websiteChannelEdit } from "@/features/channels/website/website-channel-edit"
import { wechatAuthorizationChannelEdit } from "@/features/channels/wechat/wechat-authorization-channel-edit"
import { wechatKeyChannelEdit } from "@/features/channels/wechat/wechat-key-channel-edit"
import { weComBotChannelEdit } from "@/features/channels/wecom-bot/wecom-bot-channel-edit"

const channelEditDefinitions: ChannelEditDefinitions = {
  [ChannelType.Website]: websiteChannelEdit,
  [ChannelType.Telegram]: telegramChannelEdit,
  [ChannelType.WechatKey]: wechatKeyChannelEdit,
  [ChannelType.WechatAuthorization]: wechatAuthorizationChannelEdit,
  [ChannelType.WeComBot]: weComBotChannelEdit,
}

/** 返回渠道类型的编辑页定义。 */
export function channelEditDefinition<T extends ChannelType>(type: T): ChannelEditDefinition<T> {
  return channelEditDefinitions[type]
}
