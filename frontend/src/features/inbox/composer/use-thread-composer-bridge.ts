/** 时间线与回复区之间的发送状态、引用目标、失败重试和草稿接线。 */
import { useEffect, useRef, useState } from "react"

import { MessageVisibility, type ConversationMessageReference } from "@/api"
import { useComposerDraftStore } from "@/features/inbox/state/composer-draft-context"
import { useOutgoingMessages } from "@/features/inbox/state/outgoing-message-context"
import type { OutgoingConversationDraft } from "@/features/inbox/state/outgoing-message-store"

/** 按会话绑定发送项（尚无会话编号时按 draftKey 分组），对客回复与内部备注各自保留引用目标并写入会话草稿；timeline 与 composer 分别展开到 ConversationTimeline 与 ConversationComposer。 */
export function useThreadComposerBridge(conversationKey: string, draftKey = "") {
  const prepareSendRef = useRef<(() => Promise<boolean>) | null>(null)
  const resendRef = useRef<((draft: OutgoingConversationDraft) => void) | null>(null)
  const outgoing = useOutgoingMessages(conversationKey, draftKey)
  const drafts = useComposerDraftStore()
  const draftStoreKey = conversationKey || draftKey
  const [visibility, setVisibility] = useState<MessageVisibility>(
    () => drafts.get(draftStoreKey)?.visibility ?? MessageVisibility.Shared,
  )
  const [replyTargets, setReplyTargets] = useState<
    Partial<Record<MessageVisibility, ConversationMessageReference | null>>
  >(() => drafts.get(draftStoreKey)?.replyTargets ?? {})
  const replyTo = replyTargets[visibility] ?? null

  const storedKeyRef = useRef(draftStoreKey)
  // 输入模式与引用目标变化时写入会话草稿；新聊天建立会话后草稿键改为会话编号，删除旧键下的草稿。
  useEffect(() => {
    if (storedKeyRef.current !== draftStoreKey) {
      drafts.remove(storedKeyRef.current)
      storedKeyRef.current = draftStoreKey
    }
    drafts.update(draftStoreKey, { visibility, replyTargets }, visibility)
  }, [draftStoreKey, drafts, replyTargets, visibility])

  /** 保存指定输入模式的引用目标并切到该模式。 */
  function selectReplyTarget(
    message: ConversationMessageReference | null,
    target: MessageVisibility = visibility,
  ) {
    setVisibility(target)
    setReplyTargets((current) => ({ ...current, [target]: message }))
  }

  return {
    outgoing,
    visibility,
    setVisibility,
    selectReplyTarget,
    timeline: {
      prepareSendRef,
      outgoingMessages: outgoing.messages,
      /** 按原发送逻辑编号和发送参数重新发送失败消息。 */
      onRetryFailedMessage: (draft: OutgoingConversationDraft) =>
        resendRef.current?.(
          outgoing.messages.find((message) => message.clientMessageID === draft.clientMessageID) ?? draft,
        ),
      /** 从时间线移除一条失败消息。 */
      onDiscardFailedMessage: (clientMessageID: string) => outgoing.discard(clientMessageID),
    },
    composer: {
      onBeforeSend: () => prepareSendRef.current?.() ?? Promise.resolve(true),
      resendRef,
      draftKey: draftStoreKey,
      replyTo,
      visibility,
      onReplyToChange: (message: ConversationMessageReference | null) =>
        selectReplyTarget(message),
      onSending: outgoing.start,
      onSent: outgoing.succeed,
      onFailed: outgoing.fail,
      onDiscard: outgoing.discard,
    },
  }
}
