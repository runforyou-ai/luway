/** 会话输入状态临时表：按会话与发送者保存到期计时，未刷新时自动清除，只驱动界面临时状态。 */

/** 接收端未收到刷新时清除输入状态的等待时间。 */
export const conversationTypingExpiryMs = 6_000

/** 计时器接口，测试时替换为可控实现。 */
export type ConversationTypingTimers = {
  set: (callback: () => void, delay: number) => unknown
  clear: (handle: unknown) => void
}

const browserTimers: ConversationTypingTimers = {
  set: (callback, delay) => setTimeout(callback, delay),
  clear: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
}

const emptySenders: readonly string[] = []

/** 保存各会话正在输入的发送者，并向订阅该会话的界面发布变化。 */
export class ConversationTypingStore {
  private readonly timers: ConversationTypingTimers
  private readonly conversations = new Map<string, Map<string, unknown>>()
  private readonly snapshots = new Map<string, readonly string[]>()
  private readonly listeners = new Map<string, Set<() => void>>()

  /** 创建输入状态表，默认使用浏览器计时器。 */
  constructor(timers: ConversationTypingTimers = browserTimers) {
    this.timers = timers
  }

  /** 应用一条输入状态：开始输入时刷新到期计时，停止输入时立即清除。 */
  apply(conversationID: string, senderSubjectID: string, active: boolean) {
    if (!active) {
      this.clear(conversationID, senderSubjectID)
      return
    }
    let senders = this.conversations.get(conversationID)
    if (!senders) {
      senders = new Map()
      this.conversations.set(conversationID, senders)
    }
    const existing = senders.has(senderSubjectID)
    if (existing) this.timers.clear(senders.get(senderSubjectID))
    senders.set(
      senderSubjectID,
      this.timers.set(() => this.clear(conversationID, senderSubjectID), conversationTypingExpiryMs),
    )
    if (!existing) this.publish(conversationID)
  }

  /** 清除指定发送者在会话中的输入状态。 */
  clear(conversationID: string, senderSubjectID: string) {
    const senders = this.conversations.get(conversationID)
    if (!senders?.has(senderSubjectID)) return
    this.timers.clear(senders.get(senderSubjectID))
    senders.delete(senderSubjectID)
    if (senders.size === 0) this.conversations.delete(conversationID)
    this.publish(conversationID)
  }

  /** 返回会话中正在输入的发送者，按开始输入的先后排列；内容未变化时返回同一数组。 */
  senders(conversationID: string): readonly string[] {
    return this.snapshots.get(conversationID) ?? emptySenders
  }

  /** 订阅指定会话的输入状态变化，返回取消订阅函数。 */
  subscribe(conversationID: string, listener: () => void) {
    let listeners = this.listeners.get(conversationID)
    if (!listeners) {
      listeners = new Set()
      this.listeners.set(conversationID, listeners)
    }
    listeners.add(listener)
    return () => {
      listeners.delete(listener)
      if (listeners.size === 0) this.listeners.delete(conversationID)
    }
  }

  /** 重建会话的发送者快照并通知订阅方。 */
  private publish(conversationID: string) {
    const senders = this.conversations.get(conversationID)
    if (senders) this.snapshots.set(conversationID, [...senders.keys()])
    else this.snapshots.delete(conversationID)
    for (const listener of [...(this.listeners.get(conversationID) ?? [])]) listener()
  }
}
