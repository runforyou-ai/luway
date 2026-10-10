/** 密钥接入公众号渠道的编辑页定义。 */
import type { ChannelType } from "@/api"
import type { ChannelEditDefinition } from "@/features/channels/channel-edit-definition"
import { WechatKeyConnectionForm } from "@/features/channels/wechat/wechat-key-connection-form"
import { WechatKeyInfoPanel } from "@/features/channels/wechat/wechat-key-info-panel"

/** 密钥接入公众号渠道增加连接页签，右侧显示服务器出口 IP。 */
export const wechatKeyChannelEdit: ChannelEditDefinition<typeof ChannelType.WechatKey> = {
  tabs: [{ value: "connection", form: true }],
  render: (_tab, { channel, onUpdated }) => (
    <WechatKeyConnectionForm channel={channel} onUpdated={onUpdated} />
  ),
  aside: ({ channel }) => <WechatKeyInfoPanel channel={channel} />,
}
