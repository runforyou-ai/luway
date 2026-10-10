/** 消息气泡的附属信息：被回复消息摘要、发送时间与投递状态、译文来源与切换。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import { useContactName } from "@/hooks/use-contact-name"
import { messagePreview } from "@/features/inbox/shared/message-preview"
import { cn } from "@/lib/utils"
import { languageDisplayName } from "@/lib/languages"

import { MessageSendState } from "./message-send-state"
import type { TimelineDateFormatters } from "./timeline-grouping"
import type { TimelineMessage } from "./timeline-messages"
import type { MessageTranslationView } from "./message-translation"

/** 展示被回复消息的摘要，点击跳转到原消息。 */
export function MessageReplyQuote({
  replyTo,
  outside,
  onFollow,
}: {
  replyTo: NonNullable<TimelineMessage["replyTo"]>
  outside: boolean
  onFollow: (messageID: string) => Promise<void>
}) {
  const { t } = useTranslation(["inbox", "common"])
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const contactName = useContactName()
  return (
    <button
      type="button"
      disabled={replyTo.deleted || !replyTo.id}
      onClick={() =>
        void onFollow(
          replyTo.id,
        )
      }
      className={cn(
        "mb-1.5 block w-full border-l-2 pl-2 text-left text-xs focus-visible:outline focus-visible:outline-2",
        outside
          ? "border-primary text-muted-foreground"
          : "border-accent-foreground/60 text-accent-foreground/75",
      )}
      aria-label={
        replyTo.deleted
          ? t("messageOriginalDeleted")
          : t("messageGoToOriginal")
      }
    >
      {replyTo.deleted ? (
        t("messageOriginalDeleted")
      ) : (
        <>
          <span className="block font-medium">
            {personalAgentDisplayName(
              contactName(replyTo.sender?.displayName, replyTo.sender?.contactNumber),
              replyTo.sender?.personalResponsibleName,
            ) || replyTo.externalSenderName || t("unknownSender")}
          </span>
          <span className="line-clamp-2 whitespace-pre-wrap">
            {replyTo.body ? messagePreview(replyTo.body, replyTo.sender?.identityType) : t("messageOriginalUnavailable")}
          </span>
        </>
      )}
    </button>
  )
}

/** 在文本气泡正文末行右侧展示发送时间、投递状态与失败重试。 */
export function MessageTimeMeta({
  message,
  translation,
  outgoing,
  date,
  formatters,
  muted,
  renderDeliveryState,
  onRetry,
  retryDisabled,
}: {
  message: TimelineMessage
  translation: MessageTranslationView
  outgoing: boolean
  date: Date
  formatters: TimelineDateFormatters
  muted: boolean
  renderDeliveryState: ((className?: string) => ReactNode) | undefined
  onRetry: (() => void) | undefined
  retryDisabled: boolean
}) {
  const { t } = useTranslation(["inbox", "common"])
  return (
    <div
      className={cn(
        "app-message-time float-right ml-2 inline-flex translate-y-0.5 items-center gap-1 whitespace-nowrap text-[10px]",
        muted
          ? "text-muted-foreground"
          : "text-accent-foreground/75",
      )}
    >
      <MessageTranslationLabel translation={translation} outgoing={outgoing} />
      <time
        dateTime={message.originatedAt}
        title={formatters.full.format(date)}
      >
        {formatters.clock.format(date)}
      </time>
      {renderDeliveryState ? renderDeliveryState()
      : message.deliveryStatus ? (
        <div className="inline-flex items-center gap-1.5 text-xs">
          <MessageSendState
            state={message.deliveryStatus === "failed" ? "attention" : "sending"}
            detail={message.deliveryStatus === "failed" ? t("messageSendError") : undefined}
          />
          {onRetry ? (
            <button
              type="button"
              className="underline-offset-2 hover:underline disabled:cursor-not-allowed disabled:opacity-50 disabled:no-underline"
              disabled={retryDisabled}
              onClick={onRetry}
            >
              {t("common:actions.retry")}
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

/** 在时间前展示译文来源并切换原文，翻译中与翻译失败时给出状态和重试。 */
function MessageTranslationLabel({ translation, outgoing }: { translation: MessageTranslationView; outgoing: boolean }) {
  const { t, i18n } = useTranslation("inbox")
  if (translation.status === "none") return null
  if (translation.status === "pending") return <span>{t("translationPending")}</span>
  if (translation.status === "failed") {
    return (
      <button type="button" className="underline-offset-2 hover:underline" onClick={translation.retry}>
        {t("translationFailedRetry")}
      </button>
    )
  }
  const language = languageDisplayName(translation.language, i18n.language)
  const action = translationToggleLabel(translation.showingOriginal, outgoing, t)
  return (
    <button
      type="button"
      className="underline-offset-2 hover:underline"
      aria-label={action}
      title={action}
      onClick={translation.toggle}
    >
      {translation.showingOriginal
        ? t(outgoing ? "translationCustomerReceived" : "translationOriginal")
        : t(outgoing ? "translationSentAs" : "translationFrom", { language })}
    </button>
  )
}

/** 返回切换译文的操作名：客户消息的正文是原文，客服与 AI 发出消息的正文是客户收到的内容。 */
export function translationToggleLabel(showingOriginal: boolean, outgoing: boolean, t: (key: "translationShowTranslated" | "translationShowOriginal" | "translationShowCustomerReceived") => string) {
  if (showingOriginal) return t("translationShowTranslated")
  return t(outgoing ? "translationShowCustomerReceived" : "translationShowOriginal")
}
