/** 在已登录工作台内按会话保存成员消息的发送状态。 */
import type {
  ConversationMessageData,
  CustomerReplyTranslation,
  MessageAttachment,
  ConversationMessageReference,
  MessageVisibility,
} from "@/api"
import type { MentionAllToken } from "@/lib/mention-token"

export const conversationSendingIndicatorDelay = 300

/** 输入区结构化提醒的企业成员，群聊成员同时带有聊天主体编号。 */
export type MentionTarget = {
  identityID: string
  chatSubjectID: string | null
  displayName: string
}

export type OutgoingConversationDraft = {
  clientMessageID: string
  attachment?: MessageAttachment
  visibility: MessageVisibility
  body: string
  originatedAt: string
  replyTo: ConversationMessageReference | null
  mentions: MentionTarget[]
  mentionAll: boolean
  mentionAllToken: MentionAllToken | null
  /** 对客回复发送时是否译为客户语言，translation 为预览核对过的译文；重试时原样使用。 */
  translate?: boolean
  translation?: CustomerReplyTranslation | null
}

export type OutgoingConversationMessage = OutgoingConversationDraft & {
  status: "sending" | "sent" | "failed"
  showSending: boolean
  saved: ConversationMessageData | null
}

/** 按会话分组保存发送项，以发送逻辑编号在全部会话中定位单条。 */
export class OutgoingMessageStore {
  private threads = new Map<string, OutgoingConversationMessage[]>()
  private timers = new Map<string, ReturnType<typeof setTimeout>>()
  private listeners = new Set<() => void>()

  /** 订阅发送状态变化。 */
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /** 返回当前按会话分组的发送项快照。 */
  snapshot = () => this.threads

  /** 发布新的发送状态快照。 */
  private emit() {
    this.threads = new Map(this.threads)
    for (const listener of this.listeners) listener()
  }

  /** 按发送逻辑编号就地替换一条发送项。 */
  private replace(
    clientMessageID: string,
    next: (message: OutgoingConversationMessage) => OutgoingConversationMessage,
  ) {
    for (const [conversationID, messages] of this.threads) {
      if (!messages.some((item) => item.clientMessageID === clientMessageID))
        continue
      this.threads.set(
        conversationID,
        messages.map((item) =>
          item.clientMessageID === clientMessageID ? next(item) : item,
        ),
      )
      this.emit()
      return
    }
  }

  /** 清除一条消息尚未触发的发送中提示。 */
  private clearIndicator(clientMessageID: string) {
    const timer = this.timers.get(clientMessageID)
    if (timer === undefined) return
    clearTimeout(timer)
    this.timers.delete(clientMessageID)
  }

  /** 在指定会话登记一次发送，延迟 300 毫秒后展示发送中提示。 */
  start(conversationID: string, draft: OutgoingConversationDraft) {
    const messages = this.threads.get(conversationID) ?? []
    const next: OutgoingConversationMessage = {
      ...draft,
      status: "sending",
      showSending: false,
      saved: null,
    }
    this.threads.set(
      conversationID,
      messages.some((item) => item.clientMessageID === draft.clientMessageID)
        ? messages.map((item) =>
            item.clientMessageID === draft.clientMessageID ? next : item,
          )
        : [...messages, next],
    )
    this.clearIndicator(draft.clientMessageID)
    this.timers.set(
      draft.clientMessageID,
      setTimeout(() => {
        this.timers.delete(draft.clientMessageID)
        this.replace(draft.clientMessageID, (message) =>
          message.status === "sending"
            ? { ...message, showSending: true }
            : message,
        )
      }, conversationSendingIndicatorDelay),
    )
    this.emit()
  }

  /** 按发送逻辑编号标记发送成功并保存服务端消息，编号已收尾时忽略。 */
  succeed(clientMessageID: string, saved: ConversationMessageData) {
    this.clearIndicator(clientMessageID)
    this.replace(clientMessageID, (message) => ({
      ...message,
      status: "sent",
      originatedAt: saved.originatedAt,
      saved,
    }))
  }

  /** 按发送逻辑编号丢弃一条发送项。 */
  discard(clientMessageID: string) {
    this.clearIndicator(clientMessageID)
    for (const [conversationID, messages] of this.threads) {
      const remaining = messages.filter(
        (item) => item.clientMessageID !== clientMessageID,
      )
      if (remaining.length === messages.length) continue
      if (remaining.length) this.threads.set(conversationID, remaining)
      else this.threads.delete(conversationID)
      this.emit()
      return
    }
  }

  /** 按发送逻辑编号标记发送失败，保留正文供手动重试。 */
  fail(clientMessageID: string) {
    this.clearIndicator(clientMessageID)
    this.replace(clientMessageID, (message) => ({
      ...message,
      status: "failed",
      showSending: false,
    }))
  }

  /** 把草稿会话的发送项移交给正式会话。 */
  adopt(draftConversationID: string, conversationID: string) {
    const messages = this.threads.get(draftConversationID)
    if (!messages || draftConversationID === conversationID) return
    this.threads.delete(draftConversationID)
    this.threads.set(conversationID, [
      ...(this.threads.get(conversationID) ?? []),
      ...messages,
    ])
    this.emit()
  }

  /** 删除消息窗口已经收录的发送项，其展示由窗口消息接管。 */
  reconcile(conversationID: string, messages: ConversationMessageData[]) {
    const current = this.threads.get(conversationID)
    if (!current?.length) return
    const coverage = windowCoverage(messages)
    const remaining = current.filter((item) => !coveredByWindow(item, coverage))
    if (remaining.length === current.length) return
    for (const item of current)
      if (!remaining.includes(item)) this.clearIndicator(item.clientMessageID)
    if (remaining.length) this.threads.set(conversationID, remaining)
    else this.threads.delete(conversationID)
    this.emit()
  }

  /** 清空该会话的全部发送项，用于失权。 */
  forgetConversation(conversationID: string) {
    const messages = this.threads.get(conversationID)
    if (!messages) return
    for (const message of messages) this.clearIndicator(message.clientMessageID)
    this.threads.delete(conversationID)
    this.emit()
  }

  /** 释放全部发送中提示的计时器。 */
  dispose() {
    for (const timer of this.timers.values()) clearTimeout(timer)
    this.timers.clear()
  }
}

/** 提取窗口消息的服务端编号与本人发送逻辑编号。 */
export function windowCoverage(messages: ConversationMessageData[]) {
  return {
    messageIDs: new Set(messages.map((message) => message.id)),
    clientMessageIDs: new Set(
      messages.flatMap((message) =>
        message.clientMessageId ? [message.clientMessageId] : [],
      ),
    ),
  }
}

/** 判断消息窗口是否已经收录该发送项。 */
export function coveredByWindow(
  message: OutgoingConversationMessage,
  coverage: ReturnType<typeof windowCoverage>,
) {
  return (
    coverage.clientMessageIDs.has(message.clientMessageID) ||
    Boolean(message.saved && coverage.messageIDs.has(message.saved.id))
  )
}
