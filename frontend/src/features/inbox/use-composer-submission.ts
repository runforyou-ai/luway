/** 会话消息提交与失败消息重发。 */
import { useEffect, useRef, useState, type RefObject } from "react"
import type { UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { MessageVisibility, isApiError, type CustomerReplyTranslation } from "@/api"
import type { ConversationComposerProps } from "./conversation-composer-types"
import type { ConversationComposerValues } from "./conversation-composer-schema"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import type { OutgoingConversationDraft } from "@/lib/outgoing-message-store"
import type { useComposerMentions } from "./use-composer-mentions"
import { resizeComposerInput } from "./composer-input"
import { sendComposerTextMessage } from "./composer-send"
import { useCustomerTranslation } from "./customer-translation"
import { mentionTokenPattern } from "@/lib/mention-token"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 提交当前草稿并登记失败消息的重发入口；失败消息留在时间线，由其重试或删除。 */
export function useComposerSubmission({ props, form, inputRef, disabledReason, mentionsState, typingReport }: {
  props: ConversationComposerProps
  form: UseFormReturn<ConversationComposerValues>
  inputRef: RefObject<HTMLTextAreaElement | null>
  disabledReason: string | null
  mentionsState: ReturnType<typeof useComposerMentions>
  typingReport: { stop: () => void }
}) {
  const { conversationID, conversationType, service = false, refocusAfterSubmit = false,
    visibility = MessageVisibility.MessageVisibilityShared, replyTo = null, resendRef,
    onReplyToChange, onSending, onBeforeSend, onSent, onFailed, onDiscard, onSucceeded, sendIndividualMessage,
  } = props
  const { mentions, setMentions, mentionAllToken, setMentionAllToken, mentionAll, setMentionQuery } = mentionsState
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const customerTranslation = useCustomerTranslation()
  const aliveRef = useMountedRef()
  const refocusPendingRef = useRef(false)
  const [preparing, setPreparing] = useState(false)
  const focusedReplyIDRef = useRef(replyTo?.id ?? "")
  const visibilityRef = useRef(visibility)
  visibilityRef.current = visibility
  const { isSubmitting } = form.formState
  useEffect(() => {
    resizeComposerInput(inputRef.current)
  }, [inputRef])

  useEffect(() => {
    if (isSubmitting || !refocusPendingRef.current) return
    refocusPendingRef.current = false
    form.setFocus("body")
  }, [form, isSubmitting])

  // 选择新的引用目标后聚焦输入框，进入会话时恢复的引用目标不抢焦点。
  useEffect(() => {
    const replyID = replyTo?.id ?? ""
    if (replyID === focusedReplyIDRef.current || isSubmitting) return
    focusedReplyIDRef.current = replyID
    if (replyID) form.setFocus("body")
  }, [form, isSubmitting, replyTo])

  /** 登记并发送一条消息，成功时写入结果并返回 true，失败时标记失败消息并提示原因。 */
  async function deliver(draft: OutgoingConversationDraft) {
    onSending(draft)
    try {
      const message = await sendComposerTextMessage({
        conversationType,
        service,
        conversationID,
        clientMessageID: draft.clientMessageID,
        body: draft.body,
        replyToMessageID: draft.replyTo?.id ?? "",
        visibility: draft.visibility,
        mentions: draft.mentions,
        mentionAll: draft.mentionAll,
        translate: draft.translate ?? false,
        translation: draft.translation ?? null,
        sendIndividualMessage,
      })
      onSucceeded()
      // 按发送逻辑编号写入发送结果。
      onSent(draft.clientMessageID, message)
      return true
    } catch (error) {
      if (recoverSession(error, navigate)) return false
      console.warn("发送成员会话消息失败", {
        conversationId: conversationID,
        error,
      })
      onFailed(draft.clientMessageID)
      // 回复语言变化或无法确定时刷新翻译状态，客服据此重新预览或选择回复语言。
      const languageChanged = isApiError(error) && (error.reason === "reply_language_changed" || error.reason === "customer_language_unknown")
      if (languageChanged) customerTranslation?.refresh()
      // 译文已不可用，输入框为空时失败消息回到输入框，由客服重新预览后发送。
      if (languageChanged && aliveRef.current && draft.visibility === visibilityRef.current && !form.getValues("body").trim()) {
        onDiscard?.(draft.clientMessageID)
        form.setValue("body", draft.body, { shouldDirty: true })
        setMentions(draft.mentions)
        setMentionAllToken(draft.mentionAllToken)
        onReplyToChange?.(draft.replyTo)
        resizeComposerInput(inputRef.current)
      }
      if (aliveRef.current) {
        toast.error(
          isApiError(error)
            ? apiErrorMessage(error, [
                "replyToMessageId",
                "mentionSubjectIds",
                "mentionIdentityIds",
                "body",
                "translation",
              ])
            : t("messageSendError"),
        )
      }
      return false
    }
  }

  /** 按原发送逻辑编号重新发送失败消息。 */
  async function resend(draft: OutgoingConversationDraft) {
    if (onBeforeSend && !(await onBeforeSend())) return
    await deliver(draft)
  }

  useEffect(() => {
    if (!resendRef) return
    resendRef.current = (draft) => void resend(draft)
    return () => {
      resendRef.current = null
    }
  })

  /** 按会话类型发送当前成员文本消息；translation 为预览过的对客译文。 */
  async function send(values: ConversationComposerValues, translation: CustomerReplyTranslation | null = null) {
    if (disabledReason) return
    const body = values.body.trim()
    if (!body || replyTo?.deleted) return
    // 发送前准备期间输入框只读，准备完成后按当前正文发送并清空。
    if (onBeforeSend) {
      setPreparing(true)
      try {
        if (!(await onBeforeSend())) return
      } finally {
        if (aliveRef.current) setPreparing(false)
      }
    }
    if (!aliveRef.current) return
    // 草稿正文去掉首部空白后，同步调整结构化标记的位置。
    const rawBody = form.getValues("body")
    const leadingWhitespace = rawBody.length - rawBody.trimStart().length
    const draftMentionAllToken = mentionAllToken
      ? { ...mentionAllToken, start: mentionAllToken.start - leadingWhitespace }
      : null
    // 提醒顺序决定被点名 AI 员工的发言先后，按正文中标记出现的位置排序。
    const mentionPositions = new Map(
      mentions.map((mention) => {
        const match = body.match(new RegExp(mentionTokenPattern([mention.displayName]), "u"))
        return [mention.identityID, match?.index ?? Number.MAX_SAFE_INTEGER] as const
      }),
    )
    const orderedMentions = [...mentions].sort(
      (left, right) =>
        (mentionPositions.get(left.identityID) ?? 0) - (mentionPositions.get(right.identityID) ?? 0),
    )
    const draft = {
      clientMessageID: window.crypto.randomUUID(),
      visibility,
      body,
      originatedAt: new Date().toISOString(),
      replyTo: replyTo,
      mentions: orderedMentions,
      mentionAll,
      mentionAllToken: draftMentionAllToken,
      translate: Boolean(customerTranslation?.replyNeedsTranslation && customerTranslation.translateReply),
      translation,
    }
    // 正文、提醒和引用目标随消息进入时间线，发送失败时由失败消息重试。
    form.setValue("body", "", { shouldDirty: true })
    typingReport.stop()
    setMentions([])
    setMentionAllToken(null)
    setMentionQuery(null)
    if (replyTo) onReplyToChange?.(null)
    resizeComposerInput(inputRef.current)
    await deliver(draft)
    if (!aliveRef.current) return
    refocusPendingRef.current = refocusAfterSubmit
  }

  return { send, preparing }
}
