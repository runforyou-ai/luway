/** Telegram 已保存机器人和 Webhook 信息区。 */
import { useTranslation } from "react-i18next"

import {
  TelegramWebhookStatus,
  type TelegramChannel,
} from "@/api"
import { SelectableText } from "@/components/selectable-text"
import { StatusBadge } from "@/components/status-badge"

/** 展示 Telegram 渠道已保存的只读连接信息。 */
export function TelegramChannelInfoPanel({
  channel,
}: {
  channel: TelegramChannel
}) {
  const { t } = useTranslation("channels")
  const { connection } = channel
  const username = connection.botUsername
    ? `@${connection.botUsername}`
    : "—"

  return (
    <aside className="w-full max-w-[360px] xl:sticky xl:top-6 xl:self-start">
      <h3 className="text-base font-medium">
        {t("telegramConnection.info.title")}
      </h3>
      <dl className="mt-4 border-y text-sm">
        <InfoRow
          label={t("telegramConnection.info.botDisplayName")}
          value={connection.botDisplayName ?? "—"}
        />
        <InfoRow
          label={t("telegramConnection.info.botUsername")}
          value={username}
        />
        <InfoRow
          label={t("telegramConnection.info.botId")}
          value={connection.botId ?? "—"}
        />
        <InfoRow
          label={t("telegramConnection.info.webhookUrl")}
          value={connection.webhookUrl || "—"}
          selectable
        />
        <InfoRow
          label={t("telegramConnection.info.webhookSecret")}
          value={connection.webhookSecret || "—"}
          selectable
        />
        <div className="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)]">
          <dt className="text-muted-foreground">
            {t("telegramConnection.info.webhookStatus")}
          </dt>
          <dd>
            {connection.webhookStatus ===
            TelegramWebhookStatus.TelegramWebhookStatusNormal ? (
              <StatusBadge variant="success" showDot={false}>
                {t("telegramConnection.status.normal")}
              </StatusBadge>
            ) : connection.webhookStatus ===
              TelegramWebhookStatus.TelegramWebhookStatusWaiting ? (
              <StatusBadge
                variant="muted"
                showDot={false}
                className="rounded-full px-2 text-xs"
              >
                {t("telegramConnection.status.waiting")}
              </StatusBadge>
            ) : (
              "—"
            )}
          </dd>
        </div>
      </dl>
    </aside>
  )
}

/** 展示 Telegram 信息区中的一行只读内容。 */
function InfoRow({
  label,
  value,
  selectable = false,
}: {
  label: string
  value: string
  selectable?: boolean
}) {
  return (
    <div className="grid gap-1 border-b py-3 sm:grid-cols-[7rem_minmax(0,1fr)]">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all">
        {selectable ? <SelectableText>{value}</SelectableText> : value}
      </dd>
    </div>
  )
}
