/** 会话编辑器的输入、提醒和可见范围交互状态。 */
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { ConversationType, ChannelType, MessageVisibility, type CustomerReplyTranslation } from "@/api"
import { createConversationComposerSchema, type ConversationComposerValues } from "./conversation-composer-schema"
import { reconcileMentionAllToken } from "@/lib/mention-token"
import { conversationSendingIndicatorDelay } from "@/lib/outgoing-message-store"
import { useConversationTypingReport } from "./use-conversation-typing"
import { resolveAppPlatform } from "@/platform/app-platform"
import { zodResolver } from "@/lib/zod-resolver"
import { resizeComposerInput, useFocusInputOnTyping } from "./composer-input"
import { useComposerMentions } from "./use-composer-mentions"
import { useVisibilityDrafts } from "./use-visibility-drafts"
import { useComposerSubmission } from "./use-composer-submission"
import type { ConversationComposerProps } from "./conversation-composer-types"
import { useCustomerTranslation } from "./customer-translation"
import { useComposerDraftStore } from "@/contexts/composer-draft-context"

/** 组合会话编辑器状态并提供输入交互。 */
export function useConversationComposer(props: ConversationComposerProps) {
  const { conversationID, conversationType, service = false, submitOnEnter = false, disabledReason: replyDisabledReason = null,
    visibility = MessageVisibility.MessageVisibilityShared, onVisibilityChange,
    groupParticipants, noteMentionMembers, currentIdentityID = "", customerChannel = null, draftBridgeRef, draftKey = "",
    replyTo = null, onReplyToChange,
  } = props
  const draftStore = useComposerDraftStore()
  // 进入会话时读取一次会话草稿，作为各输入模式的初始内容。
  const [initialModes] = useState(() => draftStore.get(draftKey)?.modes)
  // 渠道来源按渠道能力开放附件与输入状态，其他来源的服务会话直接支持；附件说明默认上限 4000 字。
  const customerAttachmentSupported = customerChannel ? customerChannel.attachmentSupported : service
  const customerTypingSupported =
    customerChannel ? customerChannel.type === ChannelType.ChannelTypeWebsite : service
  const customerAttachmentByteLimit = customerChannel?.attachmentByteLimit ?? 0
  const customerAttachmentCaptionLimit = customerChannel?.attachmentCaptionLimit ?? 4000
  const { t } = useTranslation("inbox")
  const schema = useMemo(
    () =>
      createConversationComposerSchema({
        bodyTooLong: t("messageBodyTooLong"),
      }),
    [t],
  )
  const form = useForm<ConversationComposerValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { body: initialModes?.[visibility]?.body ?? "" },
  })
  const inputID = `conversation-reply-${conversationID}`
  const inputRef = useRef<HTMLTextAreaElement | null>(null)
  // 移动端的提及候选和取消引用使用触屏尺寸。
  const mobile = resolveAppPlatform() === "mobile"
  const { isSubmitting } = form.formState
  const bodyValue = form.watch("body")
  const isBodyEmpty = !bodyValue.trim()
  const internalNote =
    visibility === MessageVisibility.MessageVisibilityInternal
  // 内部备注不经渠道投递，不受对客发送资格限制。
  const disabledReason = internalNote ? null : replyDisabledReason
  const groupConversation = conversationType === ConversationType.ConversationTypeGroup
  const customerConversation = service
  // 单聊与群聊向其他成员上报本人正在输入；服务会话在对客回复时向网站访客或企业成员发起人上报。
  const typingReport = useConversationTypingReport(
    conversationID,
    (groupConversation ||
      conversationType === ConversationType.ConversationTypeDirect ||
      (customerConversation && customerTypingSupported && !internalNote)) &&
      !disabledReason,
  )
  const mentionsState = useComposerMentions({
    initialDraft: initialModes?.[visibility],
    form, inputRef, typingReport, groupConversation, customerConversation, internalNote,
    groupParticipants, noteMentionMembers, currentIdentityID, noteSwitchAvailable: Boolean(onVisibilityChange),
  })
  const {
    setMentions,
    mentionsRef,
    setMentionAllToken,
    mentionQuery,
    setMentionQuery,
    activeMentionIndex,
    mentionCandidates,
    noteMentionHint,
    updateMentionQuery,
    reconcileMentions,
    selectMention,
    handleMentionKeyDown,
  } = mentionsState
  // 对客草稿与内部备注草稿各自保留正文和提醒成员，切换页签时互不覆盖。
  const { draftsRef, focusAfterSwitchRef, switchVisibility, inactiveModes } = useVisibilityDrafts({
    initialModes,
    form,
    visibility,
    onVisibilityChange,
    inputRef,
    mentionsRef,
    setMentions,
    closeMentionQuery: () => setMentionQuery(null),
  })

  const { send, preparing } = useComposerSubmission({ props, form, inputRef, disabledReason, mentionsState, typingReport })
  const latestDraftRef = useRef({ draftKey, visibility, mentionAllToken: mentionsState.mentionAllToken, inactiveModes })
  latestDraftRef.current = { draftKey, visibility, mentionAllToken: mentionsState.mentionAllToken, inactiveModes }
  // 离开会话时把各输入模式的正文和提醒写入会话草稿。
  useEffect(() => () => {
    const latest = latestDraftRef.current
    draftStore.update(latest.draftKey, {
      modes: {
        ...latest.inactiveModes(),
        [latest.visibility]: { body: form.getValues("body"), mentions: mentionsRef.current, mentionAllToken: latest.mentionAllToken },
      },
    }, latest.visibility)
  }, [draftStore, form, mentionsRef])
  const customerTranslation = useCustomerTranslation()
  // 对客回复需要翻译时提供译文预览；预览面板发送核对过的译文，Enter 与发送按钮在发送时重新翻译。
  const replyTranslationAvailable = Boolean(
    customerConversation && !internalNote && customerTranslation?.replyNeedsTranslation,
  )
  const [translationPreviewOpen, setTranslationPreviewOpen] = useState(false)
  if (translationPreviewOpen && (!replyTranslationAvailable || disabledReason)) setTranslationPreviewOpen(false)

  /** 发送预览面板中核对过的对客译文。 */
  function sendTranslation(translation: CustomerReplyTranslation) {
    setTranslationPreviewOpen(false)
    void form.handleSubmit((values) => send(values, translation))()
  }

  /** 用选中的表情替换正文当前选区，返回插入内容之后的光标位置。 */
  function insertEmoji(emoji: string) {
    const input = inputRef.current
    if (!input) return null
    const body = form.getValues("body")
    const start = input.selectionStart ?? body.length
    const end = input.selectionEnd ?? start
    const nextBody = `${body.slice(0, start)}${emoji}${body.slice(end)}`
    const nextCaret = start + emoji.length
    setMentionAllToken((current) =>
      reconcileMentionAllToken(current, body, nextBody, nextCaret),
    )
    form.setValue("body", nextBody, { shouldDirty: true })
    typingReport.input(nextBody)
    reconcileMentions(nextBody)
    resizeComposerInput(input)
    return nextCaret
  }

  useFocusInputOnTyping(inputRef, !disabledReason)

  /** 用 AI 生成的回复替换当前对客草稿，focus 为真时聚焦输入框。 */
  const applyReplySuggestion = useCallback((reply: string, focus = true) => {
    // 候选回复始终填入对客草稿，内部备注模式下先切回对客页签。
    if (visibility === MessageVisibility.MessageVisibilityInternal) {
      draftsRef.current[MessageVisibility.MessageVisibilityShared] = reply
      focusAfterSwitchRef.current = focus
      onVisibilityChange?.(MessageVisibility.MessageVisibilityShared)
      return
    }
    form.setValue("body", reply, { shouldDirty: true })
    window.requestAnimationFrame(() => {
      resizeComposerInput(inputRef.current)
      if (focus) form.setFocus("body")
    })
  }, [form, onVisibilityChange, visibility])

  useEffect(() => {
    if (!draftBridgeRef) return
    // 向 AI 助手提供读取和替换对客草稿的入口；移动端填入后返回会话，不弹出键盘。
    draftBridgeRef.current = {
      read: () =>
        internalNote
          ? (draftsRef.current[MessageVisibility.MessageVisibilityShared] ?? "")
          : form.getValues("body"),
      replace: (body) => applyReplySuggestion(body, !mobile),
    }
    return () => {
      draftBridgeRef.current = null
    }
  }, [applyReplySuggestion, draftBridgeRef, form, internalNote, mobile])

  /** 在桌面键盘上提交消息，并保留 Shift+Enter 换行。 */
  function submitFromKeyboard(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (disabledReason) return
    const composing =
      event.keyCode === 229 || event.nativeEvent.isComposing
    if (handleMentionKeyDown(event, composing)) return
    // 提示可见时 Enter 切到内部备注，Escape 关闭提示，均不发送对客消息。
    if (!composing && noteMentionHint && (event.key === "Escape" || (event.key === "Enter" && !event.shiftKey))) {
      event.preventDefault()
      if (event.key === "Enter") switchVisibility(MessageVisibility.MessageVisibilityInternal)
      else setMentionQuery(null)
      return
    }
    // 没有候选或提示时 Escape 取消引用。
    if (!composing && event.key === "Escape" && replyTo) {
      event.preventDefault()
      onReplyToChange?.(null)
      return
    }
    // Ctrl 或 Command 加 Enter 在发送前预览对客译文。
    if (!composing && event.key === "Enter" && (event.metaKey || event.ctrlKey) && replyTranslationAvailable) {
      event.preventDefault()
      if (!isBodyEmpty) setTranslationPreviewOpen(true)
      return
    }
    if (
      !submitOnEnter ||
      event.key !== "Enter" ||
      event.shiftKey ||
      composing
    ) {
      return
    }
    event.preventDefault()
    if (!form.formState.isSubmitting && !isBodyEmpty) {
      void form.handleSubmit((values) => send(values))()
    }
  }

  const [showSubmitting, setShowSubmitting] = useState(false)
  useEffect(() => {
    if (!isSubmitting) {
      setShowSubmitting(false)
      return
    }
    const timer = window.setTimeout(
      () => setShowSubmitting(true),
      conversationSendingIndicatorDelay,
    )
    return () => window.clearTimeout(timer)
  }, [isSubmitting])

  return {
    form, inputRef, inputID, bodyValue, isBodyEmpty, isSubmitting, preparing, internalNote, disabledReason,
    mobile, groupConversation, customerAttachmentSupported, customerAttachmentByteLimit, customerAttachmentCaptionLimit,
    mentionCandidates, activeMentionIndex, mentionQuery, noteMentionHint, selectMention, switchVisibility,
    setMentionAllToken, typingReport, reconcileMentions, updateMentionQuery, setMentionQuery,
    insertEmoji, applyReplySuggestion, submitFromKeyboard, showSubmitting, send,
    replyTranslationAvailable, translationPreviewOpen, setTranslationPreviewOpen, sendTranslation,
  }
}
