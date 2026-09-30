/** 把时间线窗口的读取结果同步给发送状态与正在输入状态。 */
import { useEffect, useRef } from "react"

import type { ConversationMessageListData } from "@/api"
import {
  nextTypingArrival,
  type TypingArrivalBaseline,
} from "@/features/inbox/conversation-typing-arrival"
import { useOutgoingMessageStore } from "@/contexts/outgoing-message-context"
import { clearConversationTypingSender } from "@/features/inbox/use-conversation-typing"

/** 按当前窗口收敛发送项，并清除新消息发送者的输入状态。 */
export function useTimelinePageSync(
  conversationID: string,
  currentPage: ConversationMessageListData | null,
) {
  const outgoingStore = useOutgoingMessageStore()
  useEffect(() => {
    if (!currentPage) return
    // 窗口已收录的发送项从发送状态中删除。
    outgoingStore.reconcile(conversationID, currentPage.messages)
  }, [conversationID, currentPage, outgoingStore])
  const latestMessage = currentPage?.messages[currentPage.messages.length - 1]
  const latestMessageSeq = latestMessage?.messageSeq
  const latestSenderSubjectID = latestMessage?.sender?.chatSubjectId
  const windowLoaded = Boolean(currentPage)
  const typingArrivalRef = useRef<TypingArrivalBaseline>(null)
  useEffect(() => {
    // 新消息到达后清除其发送者的正在输入状态；首次加载窗口与回看历史窗口不算新消息。
    const arrival = nextTypingArrival(typingArrivalRef.current, {
      conversationID,
      loaded: windowLoaded,
      messageSeq: latestMessageSeq,
    })
    typingArrivalRef.current = arrival.baseline
    if (arrival.arrived && latestSenderSubjectID) clearConversationTypingSender(conversationID, latestSenderSubjectID)
  }, [conversationID, latestMessageSeq, latestSenderSubjectID, windowLoaded])
}
