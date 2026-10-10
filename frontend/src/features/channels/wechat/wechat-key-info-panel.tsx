/** 密钥接入公众号渠道需要加入公众号 IP 白名单的服务器出口信息区。 */
import { useTranslation } from "react-i18next"

import type { WechatKeyChannel } from "@/api"
import { WechatServerEgressList } from "@/components/wechat/server-egress-list"

/** 展示各服务器的出口 IP，供加入公众号的 IP 白名单。 */
export function WechatKeyInfoPanel({ channel }: { channel: WechatKeyChannel }) {
  const { t } = useTranslation("channels")

  return (
    <aside className="w-full max-w-[360px] xl:sticky xl:top-6 xl:self-start">
      <h3 className="text-base font-medium">{t("wechatConnection.info.whitelistTitle")}</h3>
      <p className="mt-1 mb-4 text-sm text-muted-foreground">{t("wechatConnection.info.whitelistHelp")}</p>
      <WechatServerEgressList servers={channel.connection.servers} />
    </aside>
  )
}
