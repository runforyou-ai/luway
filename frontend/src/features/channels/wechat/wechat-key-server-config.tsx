/** 密钥接入公众号渠道的服务器地址与微信验证状态。 */
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import type { WechatKeyChannel } from "@/api"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { useDateTime } from "@/hooks/use-date-time"

/** 展示填写到公众号服务器配置中的地址，以及微信是否已按当前 Token 验证该地址。 */
export function WechatKeyServerConfig({ channel }: { channel: WechatKeyChannel }) {
  const { t } = useTranslation(["channels", "wechat", "common"])
  const { copied, copy } = useCopyFeedback<"url">()
  const { formatDateTime } = useDateTime()
  const { serverUrl, serverVerifiedAt } = channel.connection

  return (
    <section className="space-y-6">
      <div className="space-y-1">
        <h2 className="text-base font-medium">{t("wechatConnection.server.title")}</h2>
        <p className="text-sm text-muted-foreground">{t("wechatConnection.server.help")}</p>
      </div>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="wechat-channel-server-url">{t("wechatConnection.server.url")}</FieldLabel>
          <div className="flex items-center gap-2">
            <Input id="wechat-channel-server-url" value={serverUrl} readOnly className="font-mono text-muted-foreground" />
            <Button
              type="button"
              variant="outline"
              className="h-11 shrink-0"
              onClick={() => void copy(serverUrl, "url").then((done) => done || toast.error(t("wechat:copyError")))}
            >
              {copied === "url" ? t("common:actions.copied") : t("common:actions.copy")}
            </Button>
          </div>
        </Field>
        <Field>
          <FieldLabel>{t("wechatConnection.server.verification")}</FieldLabel>
          <div className="flex min-h-11 items-center">
            {serverVerifiedAt ? (
              <StatusBadge variant="success">{t("wechatConnection.server.verified")}</StatusBadge>
            ) : (
              <StatusBadge variant="muted">{t("wechatConnection.server.waiting")}</StatusBadge>
            )}
          </div>
          <FieldDescription>
            {serverVerifiedAt
              ? t("wechatConnection.server.verifiedAt", { time: formatDateTime(serverVerifiedAt) })
              : t("wechatConnection.server.waitingHelp")}
          </FieldDescription>
        </Field>
      </FieldGroup>
    </section>
  )
}
