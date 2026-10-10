/** 时间线消息行的操作入口与 AI 对客回复块。 */
import { useCallback, useMemo, useRef } from "react"
import { useTranslation } from "react-i18next"

import type { MessageVisibility, ConversationMessageReference } from "@/api"
import { Button } from "@/components/ui/button"
import type { OutgoingConversationDraft } from "@/features/inbox/state/outgoing-message-store"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 展示 AI 给出的对客回复，并提供填入回复草稿的操作。 */
function CustomerReplyBlock({
  body,
  disabledReason,
  onApply,
}: {
  body: string
  disabledReason: string | null
  onApply: (body: string) => void
}) {
  const { t } = useTranslation("inbox")
  const mobile = resolveAppPlatform() === "mobile"
  return (
    <div className="my-2 rounded-lg border bg-background p-2.5 text-foreground">
      {/* 桌面端按钮右浮动在正文末尾，末行剩余宽度足够时同行展示；移动端按钮在正文下方占满整行。 */}
      <div className="flow-root whitespace-pre-wrap break-words">
        {body}
        <Button
          type="button"
          variant="outline"
          size={mobile ? "default" : "xs"}
          className={mobile ? "mt-2.5 min-h-11 w-full" : "float-right -mt-0.5 ml-2"}
          disabled={Boolean(disabledReason)}
          title={disabledReason ?? undefined}
          onClick={() => onApply(body)}
        >
          {t("copilotApplyReply")}
        </Button>
      </div>
    </div>
  )
}

/** 返回引用稳定的消息行操作入口，入口内调用本次渲染的最新实现；对客回复渲染入口只随禁用原因变化。 */
export function useTimelineRowActions({
  onRetryFailedMessage,
  onDiscardFailedMessage,
  onReplyMessage,
  followReference,
  toggleProcess,
  onApplyReply,
  applyReplyDisabledReason,
}: {
  onRetryFailedMessage?: (message: OutgoingConversationDraft) => void
  onDiscardFailedMessage?: (clientMessageID: string) => void
  onReplyMessage?: (message: ConversationMessageReference, visibility: MessageVisibility) => void
  followReference: (messageID: string) => Promise<void>
  toggleProcess: () => void
  onApplyReply?: (body: string) => void
  applyReplyDisabledReason: string | null
}) {
  // 消息行只接收引用稳定的操作入口，入口内调用本次渲染的最新实现。
  const latestRowActions = {
    retryFailedMessage: onRetryFailedMessage,
    discardFailedMessage: onDiscardFailedMessage,
    replyMessage: onReplyMessage,
    followReference,
    toggleProcess,
    applyReply: onApplyReply,
  }
  const rowActionsRef = useRef(latestRowActions)
  rowActionsRef.current = latestRowActions
  const rowActions = useMemo(() => ({
    retryFailedMessage: (draft: OutgoingConversationDraft) => rowActionsRef.current.retryFailedMessage?.(draft),
    discardFailedMessage: (clientMessageID: string) => rowActionsRef.current.discardFailedMessage?.(clientMessageID),
    replyMessage: (message: ConversationMessageReference, visibility: MessageVisibility) =>
      rowActionsRef.current.replyMessage?.(message, visibility),
    followReference: (messageID: string) => rowActionsRef.current.followReference(messageID),
    toggleProcess: () => rowActionsRef.current.toggleProcess(),
  }), [])
  // 渲染入口只随禁用原因变化，正文组件的缓存不因每次渲染失效。
  const renderCustomerReply = useCallback(
    (language: string, code: string) => language === "customer-reply"
      ? <CustomerReplyBlock body={code.trim()} disabledReason={applyReplyDisabledReason} onApply={(body) => rowActionsRef.current.applyReply?.(body)} />
      : undefined,
    [applyReplyDisabledReason],
  )

  return { rowActions, renderCustomerReply }
}
