/** Telegram 已保存机器人与回调信息区，业务系统转发时提供转发密钥重新生成。 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  isApiError,
  regenerateTelegramGatewaySecret,
  TelegramConnectionMode,
  TelegramWebhookStatus,
  type TelegramChannel,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { SelectableText } from "@/components/selectable-text"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 展示 Telegram 渠道已保存的只读连接信息。 */
export function TelegramChannelInfoPanel({
  channel,
  onUpdated,
}: {
  channel: TelegramChannel
  onUpdated: () => void
}) {
  const { t } = useTranslation("channels")
  const navigate = useNavigate()
  const [confirming, setConfirming] = useState(false)
  const [regenerating, setRegenerating] = useState(false)
  const { connection } = channel
  const gateway =
    connection.connectionMode === TelegramConnectionMode.TelegramConnectionGateway
  const username = connection.botUsername
    ? `@${connection.botUsername}`
    : "—"

  /** 重新生成转发密钥，成功后刷新渠道详情。 */
  async function regenerate() {
    if (regenerating) return
    setRegenerating(true)
    try {
      await regenerateTelegramGatewaySecret(channel.id)
      setConfirming(false)
      onUpdated()
      toast.success(t("telegramConnection.gatewaySecret.regenerated"))
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("重新生成 Telegram 转发密钥失败", {
        channel_id: channel.id,
        error,
      })
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error)
          : t("telegramConnection.gatewaySecret.regenerateError"),
      )
    } finally {
      setRegenerating(false)
    }
  }

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
          label={
            gateway
              ? t("telegramConnection.info.forwardUrl")
              : t("telegramConnection.info.webhookUrl")
          }
          value={connection.webhookUrl || "—"}
          selectable
        />
        <InfoRow
          label={
            gateway
              ? t("telegramConnection.info.forwardSecret")
              : t("telegramConnection.info.webhookSecret")
          }
          value={connection.webhookSecret || "—"}
          selectable
        />
        <div className="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)]">
          <dt className="text-muted-foreground">
            {gateway
              ? t("telegramConnection.info.forwardStatus")
              : t("telegramConnection.info.webhookStatus")}
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
      {gateway && connection.webhookSecret ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-4"
          disabled={regenerating}
          onClick={() => setConfirming(true)}
        >
          {t("telegramConnection.gatewaySecret.regenerate")}
        </Button>
      ) : null}
      <ConfirmationDialog
        open={confirming}
        pending={regenerating}
        title={t("telegramConnection.gatewaySecret.confirmTitle")}
        description={t("telegramConnection.gatewaySecret.confirmDescription")}
        onOpenChange={setConfirming}
        onConfirm={() => void regenerate()}
      />
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
