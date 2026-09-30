/** 用可控的提醒消息读取验证新消息通知的逐条投递、去重、已读确认窗口与追赶边界。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { NewMessageWatcher } from "../src/features/notifications/new-message-watcher.ts"
import type { ConversationAttentionData, InboxConversationData } from "../src/api/index.ts"

/** 会话中的一条消息及其是否计入本人提醒。 */
type LoggedMessage = { id: string; attention: boolean }

/** 等待确认窗口到期与串行处理链执行完毕。 */
async function settle(rounds = 12) {
  for (let round = 0; round < rounds; round += 1) {
    await new Promise((resolve) => setTimeout(resolve, 2))
  }
}

/** 创建使用内存会话消息的观察器与投递记录，读取按服务端语义返回已知消息之后的提醒消息，未给出已知消息时只判断最新一条。 */
function fixture(settleWindowMs = 1) {
  const delivered: string[] = []
  const failures: unknown[] = []
  const reads: string[] = []
  const logs = new Map<string, LoggedMessage[] | null>()
  const gates: { conversations: PromiseWithResolvers<void>[]; attention: PromiseWithResolvers<void>[] } = {
    conversations: [],
    attention: [],
  }
  const attentionFailures = new Set<string>()
  const deliverFailures = new Set<string>()
  /** 按会话消息构造会话行，末条为最后一条消息。 */
  function row(id: string, log: LoggedMessage[]) {
    return { id, lastMessageId: log.at(-1)?.id ?? null } as InboxConversationData
  }
  const watcher = new NewMessageWatcher(
    {
      readConversations: async () => {
        const gate = gates.conversations.shift()
        if (gate) await gate.promise
        return [...logs].flatMap(([id, log]) => (log ? [row(id, log)] : []))
      },
      readAttention: async (conversationId, afterMessageId) => {
        reads.push(conversationId)
        const gate = gates.attention.shift()
        if (gate) await gate.promise
        if (attentionFailures.has(conversationId)) {
          attentionFailures.delete(conversationId)
          throw new Error(`读取提醒失败 ${conversationId}`)
        }
        const log = logs.get(conversationId)
        if (!log) return null
        const after = afterMessageId ? log.findIndex((item) => item.id === afterMessageId) : -1
        const range = afterMessageId ? log.slice(after + 1) : log.slice(-1)
        return {
          conversation: row(conversationId, log),
          messages: range.filter((item) => item.attention).map((item) => ({ id: item.id })),
        } as ConversationAttentionData
      },
      deliver: async (conversation, message) => {
        if (deliverFailures.has(message.id)) {
          deliverFailures.delete(message.id)
          throw new Error(`投递失败 ${message.id}`)
        }
        delivered.push(`${conversation.id}:${message.id}`)
      },
      failed: (error) => failures.push(error),
    },
    { settleWindowMs },
  )
  /** 追加会话消息，未指定时计入提醒。 */
  function append(conversationId: string, ...items: (string | LoggedMessage)[]) {
    const log = logs.get(conversationId) ?? []
    logs.set(conversationId, [...log, ...items.map((item) => (typeof item === "string" ? { id: item, attention: true } : item))])
  }
  /** 让下一次基线读取暂停，返回放行函数。 */
  function holdConversations() {
    const gate = Promise.withResolvers<void>()
    gates.conversations.push(gate)
    return () => gate.resolve()
  }
  /** 让下一次提醒读取暂停，返回放行函数。 */
  function holdAttention() {
    const gate = Promise.withResolvers<void>()
    gates.attention.push(gate)
    return () => gate.resolve()
  }
  /** 发出一条会话变化事件。 */
  function changed(conversationId: string) {
    watcher.receive({ type: "conversation_changed", conversationId, conversationType: "direct", version: 1n })
  }
  /** 发出一条连接问候事件。 */
  function hello(connectionId: string) {
    watcher.receive({ type: "server_hello", connectionId, syncHeads: {} as never })
  }
  return { watcher, delivered, failures, reads, logs, append, holdConversations, holdAttention, changed, hello, attentionFailures, deliverFailures }
}

test("同一会话连续到达的提醒消息逐条投递，未计入提醒的消息不投递，重复通知不再投递", async () => {
  const f = fixture()
  f.append("c1", "m1")
  f.hello("1")
  await settle()

  f.append("c1", "m2", { id: "m3", attention: false }, "m4")
  f.changed("c1")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, ["c1:m2", "c1:m4"])

  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, ["c1:m2", "c1:m4"])
  f.watcher.dispose()
})

test("已读确认窗口按会话从最近一次变化重新计时，不推迟其他会话", async () => {
  const f = fixture(20)
  f.append("c1", "m1")
  f.append("c2", "n1")
  f.hello("1")
  await settle()

  // c1 在窗口内持续变化，c2 只变化一次。
  f.append("c1", "m2")
  f.changed("c1")
  f.append("c2", "n2")
  f.changed("c2")
  for (let round = 0; round < 3; round += 1) {
    await new Promise((resolve) => setTimeout(resolve, 12))
    f.changed("c1")
  }
  assert.deepEqual(f.delivered, ["c2:n2"])
  assert.deepEqual(f.reads, ["c2"])

  await new Promise((resolve) => setTimeout(resolve, 30))
  await settle()
  assert.deepEqual(f.delivered, ["c2:n2", "c1:m2"])
  f.watcher.dispose()
})

test("排队等待执行期间会话再次变化，旧任务让位给重新开始的确认窗口", async () => {
  const f = fixture(20)
  f.append("c1", "m1")
  f.append("c2", "n1")
  f.hello("1")
  await settle()

  // c2 的读取卡住，c1 到期的任务排在其后。
  f.append("c2", "n2")
  const release = f.holdAttention()
  f.changed("c2")
  await new Promise((resolve) => setTimeout(resolve, 25))
  f.append("c1", "m2")
  f.changed("c1")
  await new Promise((resolve) => setTimeout(resolve, 25))
  // c1 的旧任务仍在排队时又来新消息，随后在确认窗口内被其他端读到。
  f.append("c1", "m3")
  f.changed("c1")
  release()
  await settle(3)
  assert.deepEqual(f.delivered, ["c2:n2"])
  assert.deepEqual(f.reads, ["c2"])
  f.logs.set("c1", f.logs.get("c1")!.map((item) => (item.id === "m3" ? { ...item, attention: false } : item)))

  await new Promise((resolve) => setTimeout(resolve, 30))
  await settle()
  assert.deepEqual(f.delivered, ["c2:n2", "c1:m2"])
  f.watcher.dispose()
})

test("重连追赶的历史只更新基线，之后的实时消息照常提醒", async () => {
  const f = fixture()
  f.append("c1", "m1")
  f.hello("1")
  await settle()

  // 断线期间积压四条消息，重连问候与随后的变更通知都不逐条补弹。
  f.append("c1", "m2", "m3", "m4", "m5")
  f.hello("2")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, [])

  f.append("c1", "m6")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, ["c1:m6"])
  f.watcher.dispose()
})

test("基线缺失的会话按服务端对最新一条的判断投递，失权会话清除基线且不报错", async () => {
  const f = fixture()
  f.hello("1")
  await settle()

  f.append("c9", "m1", "m2", "m3")
  f.changed("c9")
  await settle()
  assert.deepEqual(f.delivered, ["c9:m3"])

  f.logs.set("c9", null)
  f.changed("c9")
  await settle()
  assert.deepEqual(f.delivered, ["c9:m3"])
  assert.deepEqual(f.failures, [])
  f.watcher.dispose()
})

test("订阅时事件流已建立且没有问候事件，首次变化先取基线", async () => {
  const f = fixture()
  f.append("c1", "m1", "m2", "m3")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, [])

  f.append("c1", "m4")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, ["c1:m4"])
  f.watcher.dispose()
})

test("基线读取在途时重连，旧结果丢弃并按新代次重建基线", async () => {
  const f = fixture()
  f.append("c1", "m1")
  const release = f.holdConversations()
  f.hello("1")
  await settle()

  // 首轮基线读取尚未返回时断线重连，期间积压四条历史。
  f.append("c1", "m2", "m3", "m4", "m5")
  f.hello("2")
  release()
  await settle()
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, [])

  f.append("c1", "m6")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, ["c1:m6"])
  f.watcher.dispose()
})

test("会话处理在途时重连，上一代次的结果不再投递", async () => {
  const f = fixture()
  f.append("c1", "m1")
  f.hello("1")
  await settle()

  // 提醒读取期间断线重连，本轮已开始的处理不再投递。
  f.append("c1", "m2", "m3")
  const release = f.holdAttention()
  f.changed("c1")
  await new Promise((resolve) => setTimeout(resolve, 4))
  f.hello("2")
  release()
  await settle()
  assert.deepEqual(f.delivered, [])
  f.watcher.dispose()
})

test("提醒读取失败时保留基线，下次事件重新提醒该范围", async () => {
  const f = fixture()
  f.append("c1", "m1")
  f.hello("1")
  await settle()

  f.append("c1", "m2")
  f.attentionFailures.add("c1")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, [])
  assert.equal(f.failures.length, 1)

  // 同一会话再次变化时，上次失败的范围仍然被提醒。
  f.append("c1", "m3")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, ["c1:m2", "c1:m3"])
  f.watcher.dispose()
})

test("投递失败的消息不登记为已通知，基线保持原位，下次事件重新投递", async () => {
  const f = fixture()
  f.append("c1", "m1")
  f.hello("1")
  await settle()

  f.append("c1", "m2")
  f.deliverFailures.add("m2")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, [])
  assert.equal(f.failures.length, 1)

  f.append("c1", "m3")
  f.changed("c1")
  await settle()
  assert.deepEqual(f.delivered, ["c1:m2", "c1:m3"])
  f.watcher.dispose()
})
