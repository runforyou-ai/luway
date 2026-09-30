/** 消息渠道及类型扩展调用。 */
import {
  ActivateMessageChannel,
  CreateMessageChannel,
  DeactivateMessageChannel,
  GetMessageChannel,
  GetTelegramChannel,
  GetWebsiteChannel,
  ListChannelOptions,
  ListMessageChannels,
  SaveTelegramChannelConnection,
  TestTelegramChannelConnection,
  UpdateMessageChannel,
  UpdateMessageChannelReception,
  UpdateWebsiteChannelAccess,
  UpdateWebsiteChannelChatInterface,
  UpdateWebsiteChannelHelpCenter,
  UpdateWebsiteChannelHome,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import type {
  WebsiteChannel as GeneratedWebsiteChannel,
  WebsiteChannelAccess as GeneratedWebsiteChannelAccess,
  WebsiteHomeBlockType,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind, isApiError } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type WebsiteChannelAccessData =
  NonNullArrays<GeneratedWebsiteChannelAccess>

export type WebsiteChannelData = NonNullArrays<GeneratedWebsiteChannel>

export type WebsiteHomeBlockTypeId = Exclude<
  WebsiteHomeBlockType,
  WebsiteHomeBlockType.$zero
>

const listChannelOptionsBound = bind(ListChannelOptions)
const listMessageChannelsBound = bind(ListMessageChannels)

/** 读取网站渠道详情。 */
export const getWebsiteChannel = bind(GetWebsiteChannel)

/** 读取消息渠道基础信息。 */
export const getMessageChannel = bind(GetMessageChannel)

/** 读取 Telegram 渠道详情。 */
export const getTelegramChannel = bind(GetTelegramChannel)

/** 创建消息渠道。 */
export const createMessageChannel = bind(CreateMessageChannel)

/** 修改消息渠道基础信息。 */
export const updateMessageChannel = bind(UpdateMessageChannel)

/** 修改消息渠道接待设置。 */
export const updateMessageChannelReception = bind(UpdateMessageChannelReception)

/** 测试 Telegram 草稿 Token。 */
export const testTelegramChannelConnection = bind(
  TestTelegramChannelConnection,
)

/** 保存 Telegram 机器人和 Webhook 设置。 */
export const saveTelegramChannelConnection = bind(
  SaveTelegramChannelConnection,
)

/** 判断保存是否需要用户确认复用其他渠道的 Telegram Bot。 */
export function isTelegramBotReuseConfirmationError(error: unknown) {
  return (
    isApiError(error) &&
    error.kind === "conflict" &&
    error.reason === "telegram_bot_reuse_confirmation_required"
  )
}

/** 修改网站渠道聊天界面。 */
export const updateWebsiteChannelChatInterface = bind(
  UpdateWebsiteChannelChatInterface,
)

/** 修改网站渠道允许使用的网站。 */
export const updateWebsiteChannelAccess = bind(UpdateWebsiteChannelAccess)

/** 修改网站渠道帮助中心发布的知识库。 */
export const updateWebsiteChannelHelpCenter = bind(
  UpdateWebsiteChannelHelpCenter,
)

/** 修改网站渠道聊天窗口首页。 */
export const updateWebsiteChannelHome = bind(UpdateWebsiteChannelHome)

/** 停用消息渠道。 */
export const deactivateMessageChannel = bind(DeactivateMessageChannel)

/** 启用消息渠道。 */
export const activateMessageChannel = bind(ActivateMessageChannel)

/** 读取当前企业的渠道选择项。 */
export function listChannelOptions(signal?: AbortSignal) {
  return listChannelOptionsBound(signal).then((list) => list.channels)
}

/** 读取消息渠道列表。 */
export function listMessageChannels() {
  return listMessageChannelsBound().then((list) => list.channels)
}
