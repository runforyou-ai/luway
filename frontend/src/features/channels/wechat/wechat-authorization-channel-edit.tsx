/** 授权接入公众号渠道的编辑页定义。 */
import type { ChannelType } from "@/api"
import type { ChannelEditDefinition } from "@/features/channels/channel-edit-definition"
import { WechatAuthorizationConnection } from "@/features/channels/wechat/wechat-authorization-connection"

/** 授权接入公众号渠道增加连接页签。 */
export const wechatAuthorizationChannelEdit: ChannelEditDefinition<typeof ChannelType.WechatAuthorization> = {
  tabs: [{ value: "connection", form: true }],
  render: (_tab, { channel, onUpdated }) => (
    <WechatAuthorizationConnection channel={channel} onUpdated={onUpdated} />
  ),
}
