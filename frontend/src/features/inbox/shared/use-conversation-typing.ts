/** 成员端会话输入状态：输入框上报本人输入，会话头展示其他成员、AI 员工或访客正在输入。 */
import { useEffect, useMemo, useRef } from "react"
import { useTranslation } from "react-i18next"

import {
  reportConversationTyping,
  type ServiceInboxConversation,
  type GroupParticipant,
} from "@/api"
import { realtimeClient } from "@/api/realtime"
import { ConversationTypingReporter } from "@/features/inbox/shared/conversation-typing-reporter"
import { ConversationTypingStore } from "@/features/inbox/shared/conversation-typing-store"
import { useKeyedSnapshot } from "@/features/inbox/state/keyed-listeners"

const typingStore = new ConversationTypingStore()

realtimeClient.subscribe((event) => {
  if (event.type === "frame" && event.frame.type === "conversation_typing") {
    typingStore.apply(event.frame.conversationId, event.frame.senderSubjectId, event.frame.active)
  }
})

/** 新消息到达后清除其发送者的输入状态。 */
export function clearConversationTypingSender(conversationID: string, senderSubjectID: string) {
  typingStore.clear(conversationID, senderSubjectID)
}

/** 返回输入框上报本人输入状态的入口；切换会话、卸载或页面隐藏时上报停止输入。 */
export function useConversationTypingReport(conversationID: string, enabled: boolean) {
  const reporterRef = useRef<ConversationTypingReporter | null>(null)

  useEffect(() => {
    if (!enabled || !conversationID) return
    const reporter = new ConversationTypingReporter((active) => {
      // 输入状态由接收端到期清除，上报失败不影响输入与发送。
      reportConversationTyping(conversationID, active).catch(() => undefined)
    })
    reporterRef.current = reporter
    // 页面隐藏时结束本次输入。
    const stopWhenHidden = () => {
      if (document.visibilityState === "hidden") reporter.stop()
    }
    document.addEventListener("visibilitychange", stopWhenHidden)
    return () => {
      document.removeEventListener("visibilitychange", stopWhenHidden)
      reporter.stop()
      reporterRef.current = null
    }
  }, [conversationID, enabled])

  return useMemo(
    () => ({
      input: (value: string) => reporterRef.current?.input(value),
      stop: () => reporterRef.current?.stop(),
    }),
    [],
  )
}

/** 按聊天主体解析输入者名称：null 表示无法识别该输入者。 */
type TypingSenderName = (senderSubjectID: string) => string | null

/** 按群聊当前成员解析输入者名称，真人与 AI 员工同等处理，个人 AI 员工名称经 formatName 带上负责人。 */
export function groupTypingSenderName(
  participants: GroupParticipant[],
  formatName: (name: string, ownerName: string | null) => string,
): TypingSenderName {
  return (senderSubjectID) => {
    const participant = participants.find((item) => item.chatSubjectId === senderSubjectID)
    return (participant && formatName(participant.displayName.trim(), participant.personalResponsibleName)) || null
  }
}

/** 按客户与当前负责人的聊天主体解析客户会话的输入者名称，客户名称经 contactName 在没有名称时显示访客编号。 */
export function customerTypingSenderName(
  customer: ServiceInboxConversation,
  contactName: (name: string | null, number: number | null) => string,
): TypingSenderName {
  return (senderSubjectID) => {
    if (senderSubjectID === customer.requesterChatSubjectId)
      return contactName(customer.requesterName, customer.requesterContactNumber)
    if (customer.assignee && senderSubjectID === customer.assigneeChatSubjectId) return customer.assignee.displayName
    return null
  }
}

/** 返回会话头展示的正在输入文案；senderName 为 null 表示单聊或 AI 员工会话，只提示正在输入，其余会话按名称合并多人。 */
export function useConversationTypingLabel(conversationID: string, senderName: TypingSenderName | null) {
  const { t } = useTranslation("inbox")
  const senders = useKeyedSnapshot(typingStore.subscribe, conversationID, typingStore.senders)
  if (senders.length === 0) return ""
  if (!senderName) return t("typingDirect")
  const names = senders.flatMap((senderSubjectID) => {
    const name = senderName(senderSubjectID)
    if (name === null) return []
    return [name || t("unknownSender")]
  })
  if (names.length === 0) return ""
  if (names.length === 1) return t("typingOne", { name: names[0] })
  if (names.length === 2) return t("typingTwo", { first: names[0], second: names[1] })
  return t("typingMany", { count: names.length })
}
