/** 企业微信智能机器人渠道的编辑页定义。 */
import type { ChannelType } from "@/api"
import type { ChannelEditDefinition } from "@/features/channels/channel-edit-definition"
import { ChannelAccountsPanel } from "@/features/channels/wecom-bot/channel-accounts-panel"
import {
  weComBotConnectionWatched,
  WeComBotChannelConnectionForm,
} from "@/features/channels/wecom-bot/wecom-bot-channel-connection-form"

/** 企业微信智能机器人渠道增加连接与成员页签，启用期间低频刷新已保存的连接状态。 */
export const weComBotChannelEdit: ChannelEditDefinition<typeof ChannelType.WeComBot> = {
  tabs: [
    { value: "connection", form: true },
    { value: "members", form: false },
  ],
  render: (tab, { channel, onUpdated, onSavingChange }) =>
    tab === "members" ? (
      <ChannelAccountsPanel channelID={channel.id} />
    ) : (
      <WeComBotChannelConnectionForm channel={channel} onUpdated={onUpdated} onSavingChange={onSavingChange} />
    ),
  refetchInterval: (channel, saving) => (!saving && weComBotConnectionWatched(channel) ? 8_000 : false),
}
