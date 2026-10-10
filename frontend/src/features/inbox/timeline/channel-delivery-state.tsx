/** 展示客户消息的外部投递状态并处理人工确认。 */
import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { MoreHorizontalIcon } from "lucide-react"
import { toast } from "sonner"
import {
  ChannelDeliveryResolution,
  ChannelDeliveryStatus,
  isApiError,
  resolveChannelMessageDelivery,
  type ChannelMessageDelivery,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { cn } from "@/lib/utils"
import { MessageSendState } from "./message-send-state"

/** 在气泡内的时间右侧展示投递状态和处理入口。 */
export function ChannelDeliveryState({
  conversationID, delivery, localFailed = false,
  onRetryLocal, retryLocalDisabled = false, className,
}: {
  conversationID: string
  delivery: ChannelMessageDelivery | null
  localFailed?: boolean
  onRetryLocal?: () => void
  retryLocalDisabled?: boolean
  className?: string
}) {
  const { t } = useTranslation(["inbox", "common"])
  const invalidate = useResourceInvalidator()
  const [confirmRetry, setConfirmRetry] = useState(false)
  const review = delivery?.status === ChannelDeliveryStatus.ChannelDeliveryNeedsReview
  const retryable = delivery?.canRetry
  const resolution = useMutation({
    mutationFn: async ({ deliveryID, ...input }: { deliveryID: string; resolution: ChannelDeliveryResolution; confirmDuplicateRisk: boolean }) => {
      await resolveChannelMessageDelivery(conversationID, deliveryID, input)
      await invalidate(resourceKeys.conversationMessages(conversationID))
    },
    onError: (error) => toast.error(isApiError(error) ? error.message : t("deliveryResolveError")),
  })
  const busy = resolution.isPending

  /** 提交人工处理意图并重读消息窗口中的持久状态。 */
  function resolve(
    next: ChannelDeliveryResolution,
    confirmDuplicateRisk = false,
  ) {
    if (!delivery) return
    resolution.mutate(
      { deliveryID: delivery.id, resolution: next, confirmDuplicateRisk },
      { onSuccess: () => setConfirmRetry(false) },
    )
  }

  // 本地提交、排队和自动重试统一为发送中，异常原因只在提示中展示。
  const uncertain = review || delivery?.status === ChannelDeliveryStatus.ChannelDeliveryUncertain
  const failed = localFailed || delivery?.status === ChannelDeliveryStatus.ChannelDeliveryFailed
  const state = failed || uncertain || delivery?.paused
    ? "attention"
    : delivery?.status === ChannelDeliveryStatus.ChannelDeliverySent ? "sent" : "sending"
  // 发送成功后只保留消息时间，发送中和异常状态继续提示。
  if (state === "sent") return null
  let detail: string | undefined
  if (localFailed) detail = t("messageSendError")
  else if (uncertain) detail = t("deliveryUnconfirmed")
  else if (delivery?.paused) detail = t("deliveryError_channel_disabled")
  else if (failed) detail = delivery?.lastError
    ? t("deliveryError", { context: delivery.lastError })
    : t("messageSendError")
  // 已发出部分内容的消息说明发送进度，有异常原因时附在其后。
  if (delivery?.partiallySent) detail = detail ? t("deliveryPartiallySent", { detail }) : t("deliveryPartiallySentRest")

  return (
    <div className={cn("inline-flex items-center gap-1.5 text-xs", className)}>
      <MessageSendState state={state} detail={detail} />
      {localFailed && onRetryLocal ? (
        <button type="button" disabled={retryLocalDisabled} onClick={onRetryLocal}>{t("common:actions.retry")}</button>
      ) : null}
      {retryable ? (
        <button type="button" disabled={busy} onClick={() => {
          // 未知结果重试前说明可能重复，包括人工标记失败后的再次重试。
          if (review || delivery?.lastError === "unknown_result") setConfirmRetry(true)
          else resolve(ChannelDeliveryResolution.ChannelDeliveryRetry)
        }}>{t("common:actions.retry")}</button>
      ) : null}
      {review ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-5" disabled={busy} aria-label={t("deliveryActions")}>
              <MoreHorizontalIcon className="size-3" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => resolve(ChannelDeliveryResolution.ChannelDeliveryConfirmSent)}>
              {t("deliveryConfirmSent")}
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => resolve(ChannelDeliveryResolution.ChannelDeliveryConfirmFailed)}>
              {t("deliveryConfirmFailed")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
      <ConfirmationDialog
        open={confirmRetry}
        pending={busy}
        title={t("deliveryRetryTitle")}
        description={t("deliveryRetryRisk")}
        destructive={false}
        onOpenChange={setConfirmRetry}
        onConfirm={() => resolve(ChannelDeliveryResolution.ChannelDeliveryRetry, true)}
      />
    </div>
  )
}
