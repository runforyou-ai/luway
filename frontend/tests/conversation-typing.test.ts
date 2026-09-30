/** 验证会话输入状态的上报节奏与接收端到期清除。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import {
  ConversationTypingStore,
  conversationTypingExpiryMs,
  type ConversationTypingTimers,
} from "../src/features/inbox/conversation-typing-store.ts"
import {
  nextTypingArrival,
  type TypingArrivalBaseline,
} from "../src/features/inbox/conversation-typing-arrival.ts"
import {
  ConversationTypingReporter,
  conversationTypingIdleMs,
  conversationTypingRefreshMs,
} from "../src/features/inbox/conversation-typing-reporter.ts"

/** 可手动推进时间的计时器。 */
function manualClock() {
  let now = 0
  let nextID = 0
  const pending = new Map<number, { at: number; callback: () => void }>()
  const timers: ConversationTypingTimers = {
    set: (callback, delay) => {
      nextID += 1
      pending.set(nextID, { at: now + delay, callback })
      return nextID
    },
    clear: (handle) => {
      pending.delete(handle as number)
    },
  }
  /** 推进时间并按到期先后执行计时回调。 */
  function advance(ms: number) {
    const target = now + ms
    for (;;) {
      const due = [...pending.entries()].filter(([, timer]) => timer.at <= target).sort((a, b) => a[1].at - b[1].at)[0]
      if (!due) break
      pending.delete(due[0])
      now = due[1].at
      due[1].callback()
    }
    now = target
  }
  return { timers, advance, now: () => now }
}

test("接收端按发送者保存输入状态，停止输入立即清除，未刷新时到期清除", () => {
  const clock = manualClock()
  const store = new ConversationTypingStore(clock.timers)
  let notified = 0
  store.subscribe("c1", () => { notified += 1 })

  store.apply("c1", "a", true)
  store.apply("c1", "b", true)
  const snapshot = store.senders("c1")
  assert.deepEqual(snapshot, ["a", "b"])
  assert.equal(notified, 2)

  // 刷新已有发送者不改变快照，也不通知。
  clock.advance(conversationTypingExpiryMs - 1)
  store.apply("c1", "a", true)
  assert.equal(store.senders("c1"), snapshot)
  assert.equal(notified, 2)

  // b 未刷新先到期，a 刷新后继续保留。
  clock.advance(1)
  assert.deepEqual(store.senders("c1"), ["a"])
  store.apply("c1", "a", false)
  assert.deepEqual(store.senders("c1"), [])
  assert.deepEqual(store.senders("c2"), [])

  // 新消息到达后清除发送者，重复清除不再通知。
  store.apply("c1", "a", true)
  const before = notified
  store.clear("c1", "a")
  store.clear("c1", "a")
  assert.equal(notified, before + 1)
})

test("上报器首次输入立即上报，持续输入按间隔刷新，停顿后上报停止", () => {
  const clock = manualClock()
  const reports: boolean[] = []
  const reporter = new ConversationTypingReporter((active) => reports.push(active), clock.timers, clock.now)

  reporter.input("你")
  reporter.input("你好")
  assert.deepEqual(reports, [true])
  clock.advance(conversationTypingRefreshMs)
  reporter.input("你好啊")
  assert.deepEqual(reports, [true, true])

  // 停顿超过等待时间后上报停止，之后停止不再重复上报。
  clock.advance(conversationTypingIdleMs)
  assert.deepEqual(reports, [true, true, false])
  reporter.stop()
  assert.deepEqual(reports, [true, true, false])

  // 清空输入与发送立即上报停止。
  reporter.input("再")
  reporter.input("  ")
  assert.deepEqual(reports, [true, true, false, true, false])
  reporter.input("发")
  reporter.stop()
  assert.deepEqual(reports, [true, true, false, true, false, true, false])
  clock.advance(conversationTypingIdleMs)
  assert.equal(reports.length, 7)
})

test("只有窗口内出现更晚的消息才算新消息到达", () => {
  let baseline: TypingArrivalBaseline = null
  /** 推进一次窗口状态并返回是否判定为新消息。 */
  function advance(window: { conversationID: string; loaded: boolean; messageSeq?: string }) {
    const result = nextTypingArrival(baseline, window)
    baseline = result.baseline
    return result.arrived
  }

  // 窗口尚未加载时不建立基线，首次加载到的最后一条消息不是新消息。
  assert.equal(advance({ conversationID: "c1", loaded: false }), false)
  assert.equal(baseline, null)
  assert.equal(advance({ conversationID: "c1", loaded: true, messageSeq: "7" }), false)
  assert.equal(advance({ conversationID: "c1", loaded: true, messageSeq: "7" }), false)

  // 新消息到达算一次，回看更早的历史窗口不降低基线也不算新消息。
  assert.equal(advance({ conversationID: "c1", loaded: true, messageSeq: "8" }), true)
  assert.equal(advance({ conversationID: "c1", loaded: true, messageSeq: "3" }), false)
  assert.equal(advance({ conversationID: "c1", loaded: true, messageSeq: "8" }), false)
  assert.equal(advance({ conversationID: "c1", loaded: true, messageSeq: "9" }), true)

  // 切换会话重建基线，空会话加载后的第一条消息算新消息。
  assert.equal(advance({ conversationID: "c2", loaded: true, messageSeq: "20" }), false)
  assert.equal(advance({ conversationID: "c3", loaded: true }), false)
  assert.equal(advance({ conversationID: "c3", loaded: true, messageSeq: "1" }), true)
})
