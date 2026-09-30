/** 会话输入状态上报节奏：输入变化时上报开始，持续输入按间隔刷新，停顿、清空或离开时上报停止。 */
import type { ConversationTypingTimers } from "@/features/inbox/conversation-typing-store"

/** 持续输入期间再次上报开始输入的最短间隔。 */
export const conversationTypingRefreshMs = 3_000

/** 无输入后上报停止输入的等待时间。 */
export const conversationTypingIdleMs = 5_000

const browserTimers: ConversationTypingTimers = {
  set: (callback, delay) => setTimeout(callback, delay),
  clear: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
}

/** 按共用契约决定何时上报输入状态，上报结果由调用方处理。 */
export class ConversationTypingReporter {
  private readonly report: (active: boolean) => void
  private readonly timers: ConversationTypingTimers
  private readonly now: () => number
  private active = false
  private reportedAt = 0
  private idleTimer: unknown = undefined

  /** 创建上报器，默认使用浏览器计时器与当前时间。 */
  constructor(
    report: (active: boolean) => void,
    timers: ConversationTypingTimers = browserTimers,
    now: () => number = Date.now,
  ) {
    this.report = report
    this.timers = timers
    this.now = now
  }

  /** 处理输入内容变化；内容为空时上报停止，否则按刷新间隔上报开始并重置停顿计时。 */
  input(value: string) {
    if (!value.trim()) {
      this.stop()
      return
    }
    const now = this.now()
    if (!this.active || now - this.reportedAt >= conversationTypingRefreshMs) {
      this.active = true
      this.reportedAt = now
      this.report(true)
    }
    this.timers.clear(this.idleTimer)
    this.idleTimer = this.timers.set(() => this.stop(), conversationTypingIdleMs)
  }

  /** 结束本次输入，之前上报过开始输入时上报停止。 */
  stop() {
    this.timers.clear(this.idleTimer)
    this.idleTimer = undefined
    if (!this.active) return
    this.active = false
    this.report(false)
  }
}
