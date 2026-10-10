/** Telegram 渠道的编辑页定义。 */
import type { ChannelType } from "@/api"
import type { ChannelEditDefinition } from "@/features/channels/channel-edit-definition"
import { TelegramChannelConnectionForm } from "@/features/channels/telegram/telegram-channel-connection-form"
import { TelegramChannelInfoPanel } from "@/features/channels/telegram/telegram-channel-info-panel"

/** Telegram 渠道增加连接页签，右侧显示接入信息。 */
export const telegramChannelEdit: ChannelEditDefinition<typeof ChannelType.Telegram> = {
  tabs: [{ value: "connection", form: true }],
  render: (_tab, { channel, onUpdated }) => (
    <TelegramChannelConnectionForm channel={channel} onUpdated={onUpdated} />
  ),
  aside: ({ channel, onUpdated }) => (
    <TelegramChannelInfoPanel channel={channel} onUpdated={onUpdated} />
  ),
}
