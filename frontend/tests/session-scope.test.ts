/** 验证登录会话代次对调用结果的交付约束与会话边界通知。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { enqueueConversationUnreadChange } from "../src/api/conversation-read-queue.ts"
import {
  advanceSessionGeneration,
  currentSessionGeneration,
  settleInSessionGeneration,
  subscribeSessionGeneration,
} from "../src/api/session-scope.ts"

/** 等待已排队的微任务与 I/O 回调执行完毕。 */
function flush() {
  return new Promise((resolve) => setImmediate(resolve))
}

test("当前代次的成功与失败结果照常交付", async () => {
  const generation = currentSessionGeneration()
  assert.equal(await settleInSessionGeneration(generation, Promise.resolve(1)), 1)
  await assert.rejects(settleInSessionGeneration(generation, Promise.reject(new Error("failed"))), /failed/)
})

test("代次变化后旧调用的成功与失败结果都不交付", async () => {
  const generation = currentSessionGeneration()
  const success = Promise.withResolvers<number>()
  const failure = Promise.withResolvers<number>()
  const settled: string[] = []
  settleInSessionGeneration(generation, success.promise).then(() => settled.push("success"), () => settled.push("success"))
  settleInSessionGeneration(generation, failure.promise).then(() => settled.push("failure"), () => settled.push("failure"))
  advanceSessionGeneration()
  success.resolve(1)
  // 旧会话的登录失效错误不能交给新会话的恢复逻辑。
  failure.reject(new Error("login required"))
  await flush()
  assert.deepEqual(settled, [])
})

test("提升代次按订阅顺序通知，订阅方异常不阻断后续订阅方", (t) => {
  t.mock.method(console, "error", () => {})
  const calls: string[] = []
  const stopFirst = subscribeSessionGeneration(() => {
    calls.push("first")
    throw new Error("listener failed")
  })
  const stopSecond = subscribeSessionGeneration(() => calls.push(`second:${currentSessionGeneration()}`))
  const before = currentSessionGeneration()
  advanceSessionGeneration()
  stopFirst()
  advanceSessionGeneration()
  stopSecond()
  advanceSessionGeneration()
  assert.deepEqual(calls, ["first", `second:${before + 1}`, `second:${before + 2}`])
})

test("新登录会话的未读标记不等待旧会话未完成的排队链", async () => {
  const writes: string[] = []
  void enqueueConversationUnreadChange("conversation", () => {
    writes.push("old")
    return new Promise<void>(() => {})
  })
  await flush()
  advanceSessionGeneration()
  await enqueueConversationUnreadChange("conversation", async () => {
    writes.push("new")
  })
  assert.deepEqual(writes, ["old", "new"])
})
