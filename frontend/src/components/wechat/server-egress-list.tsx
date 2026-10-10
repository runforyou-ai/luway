/** 微信 IP 白名单使用的服务器出口列表：各服务器主机名、在线状态与配置的出口 IP，提供复制全部出口 IP。 */
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import type { WechatServer } from "@/api"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"

/** 列出各服务器的主机名、在线状态与配置的出口 IP，有出口 IP 时提供复制全部。 */
export function WechatServerEgressList({ servers }: { servers: WechatServer[] }) {
  const { t } = useTranslation(["wechat", "common"])
  const { copied, copy } = useCopyFeedback<"all">()
  const addresses = [...new Set(servers.map((server) => server.egressIp).filter(Boolean))]

  if (servers.length === 0) {
    return <p className="text-sm text-muted-foreground">{t("servers.none")}</p>
  }
  return (
    <div className="space-y-4">
      <ul className="divide-y border-y text-sm">
        {servers.map((server, index) => (
          <li key={`${server.hostname}-${index}`} className="flex min-h-12 flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2">
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate">{server.hostname}</span>
              {!server.online ? (
                <StatusBadge variant="muted" showDot={false}>
                  {t("servers.offline")}
                </StatusBadge>
              ) : null}
            </span>
            {server.egressIp ? (
              <span className="font-mono select-all">{server.egressIp}</span>
            ) : (
              <span className="text-destructive">{t("servers.egressMissing")}</span>
            )}
          </li>
        ))}
      </ul>
      {addresses.length > 0 ? (
        <div className="flex justify-end">
          <Button
            type="button"
            variant="outline"
            className="touch:min-h-11 touch:flex-1"
            onClick={() => void copy(addresses.join("\n"), "all").then((done) => done || toast.error(t("copyError")))}
          >
            {copied === "all" ? t("common:actions.copied") : t("servers.copyAll")}
          </Button>
        </div>
      ) : null}
    </div>
  )
}
