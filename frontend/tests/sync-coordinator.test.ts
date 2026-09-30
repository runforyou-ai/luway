/** 用假计时器与可控探针验证同步协调器的通知失效映射、合并窗口和兜底校验。 */
import assert from "node:assert/strict"
import { test, type TestContext } from "node:test"
import type { SyncHeads } from "../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { SyncCoordinator } from "../src/features/session/sync-coordinator.ts"

const heads: SyncHeads = { conversationCount: 2, conversationChecksum: "11", identityProfileVersion: "3", pinOrderVersion: "4" }

/** 等待已排队的微任务执行完毕。 */
function flush() {
  return new Promise((resolve) => setImmediate(resolve))
}

/** 构造使用假计时器与可控探针的协调器，返回已失效 key、按条件失效的聊天列表与探针请求。 */
function setup(t: TestContext) {
  t.mock.timers.enable({ apis: ["setTimeout", "setInterval"] })
  const invalidated: string[] = []
  const chatMatchers: ((queryKey: readonly unknown[]) => boolean)[] = []
  const probes: PromiseWithResolvers<SyncHeads>[] = []
  const failures: unknown[] = []
  const retries = { count: 0 }
  const coordinator = new SyncCoordinator({
    invalidate: (key, matches) => {
      if (matches) chatMatchers.push(matches)
      else invalidated.push(JSON.stringify(key))
    },
    retry: () => {
      retries.count += 1
    },
    readHeads: () => {
      const probe = Promise.withResolvers<SyncHeads>()
      probes.push(probe)
      return probe.promise
    },
    failed: (error) => failures.push(error),
  })
  t.after(() => coordinator.dispose())
  return { coordinator, invalidated, chatMatchers, probes, failures, retries }
}

/** 统计指定 key 被失效的次数。 */
function count(invalidated: string[], key: unknown[]) {
  return invalidated.filter((value) => value === JSON.stringify(key)).length
}

test("合并窗口内同一会话的多条通知只失效一次", (t) => {
  const { coordinator, invalidated, chatMatchers } = setup(t)
  for (const version of [1n, 2n, 3n]) {
    coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "direct", version })
  }
  assert.deepEqual(invalidated, [])
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary", "c1"]), 1)
  assert.equal(count(invalidated, ["conversation-messages", "c1"]), 1)
  assert.equal(chatMatchers.length, 1)
})

test("持续到达的通知按固定窗口分批失效，不被后续通知一直推迟", (t) => {
  const { coordinator, invalidated } = setup(t)
  for (let index = 0; index < 10; index += 1) {
    coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "direct", version: BigInt(index) })
    t.mock.timers.tick(100)
  }
  assert.equal(count(invalidated, ["conversation-summary", "c1"]), 3)
})

// 本人身份资料变化时失效身份与展示本人名称、头像的成员目录。
const identityProfileKeys = [["identity"], ["users"], ["user"], ["colleagues"], ["team-members"]].map((key) => JSON.stringify(key))

test("通知种类映射到对应资源", (t) => {
  const { coordinator, invalidated } = setup(t)
  coordinator.receive({ type: "identity_profile_changed", version: 4n })
  t.mock.timers.tick(300)
  assert.deepEqual(invalidated, identityProfileKeys)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_state_changed", conversationId: "c1", version: 2n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary", "c1"]), 1)
  assert.equal(count(invalidated, ["conversation-mentions", "c1"]), 1)
  assert.equal(count(invalidated, ["group-conversation", "c1"]), 1)
  assert.equal(count(invalidated, ["conversation-messages", "c1"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_removed", conversationId: "c2" })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary", "c2"]), 1)
  assert.equal(count(invalidated, ["group-conversation", "c2"]), 1)
  assert.equal(count(invalidated, ["inbox-conversations"]), 1)
  assert.equal(count(invalidated, ["inbox-search"]), 1)
})

test("会话变更按会话类型只重读相关列表与服务记录", (t) => {
  const { coordinator, invalidated, chatMatchers } = setup(t)
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "channel", version: 1n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["inbox", { scope: "pending" }]), 1)
  assert.equal(count(invalidated, ["inbox", { scope: "all" }]), 1)
  assert.equal(chatMatchers.length, 0)
  assert.equal(count(invalidated, ["inbox-attention"]), 1)
  assert.equal(count(invalidated, ["knowledge-gaps", "responsible-pending-count"]), 0)
  assert.equal(count(invalidated, ["agent-service-sessions"]), 1)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "c2", conversationType: "group", version: 1n })
  t.mock.timers.tick(300)
  assert.equal(chatMatchers.length, 1)
  assert.equal(count(invalidated, ["inbox", { scope: "pending" }]), 0)
  assert.equal(count(invalidated, ["agent-service-sessions"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "t1", conversationType: "copilot", version: 1n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["service-copilot-threads"]), 1)
  assert.equal(count(invalidated, ["conversation-messages", "t1"]), 1)
  assert.equal(invalidated.filter((key) => key.startsWith('["inbox')).length, 0)
})

test("待补知识变化与重新连接时重读待补知识、报表概览和问题会话", (t) => {
  const { coordinator, invalidated } = setup(t)
  coordinator.receive({ type: "knowledge_gaps_changed" })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["knowledge-gaps"]), 1)
  assert.equal(count(invalidated, ["ai-performance-report"]), 1)
  assert.equal(count(invalidated, ["ai-performance-issues"]), 1)

  invalidated.length = 0
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: heads })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["knowledge-gaps"]), 1)
})

test("客服报表变化时重读 AI 与团队表现的报表、拆分和问题会话，不重读待补知识", (t) => {
  const { coordinator, invalidated } = setup(t)
  coordinator.receive({ type: "service_reports_changed" })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["ai-performance-report"]), 1)
  assert.equal(count(invalidated, ["ai-performance-breakdowns"]), 1)
  assert.equal(count(invalidated, ["ai-performance-issues"]), 1)
  assert.equal(count(invalidated, ["team-performance-report"]), 1)
  assert.equal(count(invalidated, ["team-performance-members"]), 1)
  assert.equal(count(invalidated, ["team-performance-breakdowns"]), 1)
  assert.equal(count(invalidated, ["team-performance-issues"]), 1)
  assert.equal(count(invalidated, ["service-issue"]), 1)
  assert.equal(count(invalidated, ["knowledge-gaps"]), 0)
})

test("助理记忆变更只重读该助理的记忆，连接问候重读全部助理记忆", (t) => {
  const { coordinator, invalidated } = setup(t)
  coordinator.receive({ type: "assistant_memory_changed", assistantId: "a1" })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["assistant-memories", "a1"]), 1)
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: heads })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["assistant-memories"]), 1)
})

test("会话变更只重读该类型会话具有的资源", (t) => {
  const { coordinator, invalidated } = setup(t)
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "group", version: 1n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["group-conversation", "c1"]), 1)
  assert.equal(count(invalidated, ["direct-conversation"]), 0)
  assert.equal(count(invalidated, ["service-summaries", "c1"]), 0)
  assert.equal(count(invalidated, ["requester-profile", "c1"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "c2", conversationType: "direct", version: 1n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["direct-conversation"]), 1)
  assert.equal(count(invalidated, ["group-conversation", "c2"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "c3", conversationType: "channel", version: 1n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["requester-profile", "c3"]), 1)
  assert.equal(count(invalidated, ["service-summaries", "c3"]), 1)
  assert.equal(count(invalidated, ["service-copilot-threads", "c3"]), 0)
  assert.equal(count(invalidated, ["direct-conversation"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "c4", conversationType: "agent", version: 1n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["service-summaries", "c4"]), 1)
  assert.equal(count(invalidated, ["service-business-queries", "c4"]), 1)
  assert.equal(count(invalidated, ["requester-profile", "c4"]), 1)
})

test("会话变更按变化类别收窄重读范围", (t) => {
  const { coordinator, invalidated } = setup(t)
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "channel", version: 1n, changes: ["timeline"] })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary", "c1"]), 1)
  assert.equal(count(invalidated, ["conversation-messages", "c1"]), 1)
  for (const key of [["requester-profile", "c1"], ["service-business-queries", "c1"], ["service-summaries", "c1"], ["agent-service-sessions"]]) {
    assert.equal(count(invalidated, key), 0, JSON.stringify(key))
  }

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "channel", version: 2n, changes: ["service"] })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["service-business-queries", "c1"]), 1)
  assert.equal(count(invalidated, ["service-summaries", "c1"]), 1)
  assert.equal(count(invalidated, ["agent-service-sessions"]), 1)
  assert.equal(count(invalidated, ["requester-profile", "c1"]), 0)
  assert.equal(count(invalidated, ["conversation-messages", "c1"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "channel", version: 3n, changes: [] })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary", "c1"]), 1)
  assert.equal(count(invalidated, ["conversation-messages", "c1"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "g1", conversationType: "group", version: 1n, changes: ["timeline"] })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["group-conversation", "g1"]), 0)
  coordinator.receive({ type: "conversation_changed", conversationId: "g1", conversationType: "group", version: 2n, changes: ["participants"] })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["group-conversation", "g1"]), 1)
  assert.equal(count(invalidated, ["conversation-messages", "g1"]), 2)
})

test("聊天列表只重读展示该类型会话的列表", (t) => {
  const { coordinator, chatMatchers } = setup(t)
  coordinator.receive({ type: "conversation_changed", conversationId: "g1", conversationType: "group", version: 1n, changes: ["timeline"] })
  t.mock.timers.tick(300)
  const [groupMatcher] = chatMatchers
  assert.equal(groupMatcher(["inbox", { scope: "chat", kinds: ["group"] }]), true)
  assert.equal(groupMatcher(["inbox", { scope: "chat", kinds: [] }]), true)
  assert.equal(groupMatcher(["inbox", { scope: "chat" }]), true)
  assert.equal(groupMatcher(["inbox", { scope: "chat", kinds: ["direct", "agent"] }]), false)

  coordinator.receive({ type: "conversation_changed", conversationId: "a1", conversationType: "agent", version: 1n, changes: ["timeline"] })
  coordinator.receive({ type: "conversation_changed", conversationId: "d1", conversationType: "direct", version: 1n, changes: ["timeline"] })
  t.mock.timers.tick(300)
  assert.equal(chatMatchers.length, 2)
  assert.equal(chatMatchers[1](["inbox", { scope: "chat", kinds: ["direct", "agent"] }]), true)
  assert.equal(chatMatchers[1](["inbox", { scope: "chat", kinds: ["group"] }]), false)
})

test("探针不一致时失效全部会话资源前缀", async (t) => {
  const { coordinator, invalidated, probes } = setup(t)
  coordinator.start()
  probes[0].resolve(heads)
  await flush()
  t.mock.timers.tick(300)
  for (const prefix of [
    "requester-profile",
    "service-business-queries",
    "service-summaries",
    "agent-service-sessions",
    "group-conversation",
    "direct-conversation",
    "service-copilot-threads",
  ]) {
    assert.equal(count(invalidated, [prefix]), 1, prefix)
  }
})

test("同批次已被前缀覆盖的会话 key 不重复失效", async (t) => {
  const { coordinator, invalidated, probes } = setup(t)
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "direct", version: 1n })
  coordinator.start()
  probes[0].resolve(heads)
  await flush()
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary"]), 1)
  assert.equal(count(invalidated, ["conversation-summary", "c1"]), 0)
  assert.equal(count(invalidated, ["inbox"]), 1)
})

test("暂停后不再周期探针，重新开始时立即校验一次", async (t) => {
  const { coordinator, probes } = setup(t)
  coordinator.start()
  probes[0].resolve(heads)
  await flush()
  coordinator.suspend()
  t.mock.timers.tick(90_000)
  assert.equal(probes.length, 1)

  coordinator.start()
  assert.equal(probes.length, 2)
  probes[1].resolve(heads)
  await flush()
  t.mock.timers.tick(30_000)
  assert.equal(probes.length, 3)
})

test("探针值首次取得时重读，之后只重读不一致的部分", async (t) => {
  const { coordinator, invalidated, probes } = setup(t)
  coordinator.start()
  probes[0].resolve(heads)
  await flush()
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary"]), 1)
  assert.equal(count(invalidated, ["identity"]), 1)

  invalidated.length = 0
  t.mock.timers.tick(30_000)
  probes[1].resolve({ ...heads })
  await flush()
  t.mock.timers.tick(300)
  assert.deepEqual(invalidated, [])

  t.mock.timers.tick(30_000)
  probes[2].resolve({ ...heads, conversationChecksum: "12" })
  await flush()
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary"]), 1)
  assert.equal(count(invalidated, ["identity"]), 0)

  invalidated.length = 0
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: { ...heads, conversationChecksum: "12", identityProfileVersion: "4" } })
  t.mock.timers.tick(300)
  // 待补知识与助理记忆没有探针，每次连接问候都重读。
  assert.deepEqual(invalidated, [...identityProfileKeys, '["knowledge-gaps"]', '["knowledge-gap"]', '["ai-performance-report"]', '["ai-performance-breakdowns"]', '["ai-performance-issues"]', '["assistant-memories"]'])
})

test("连接问候与探针共用上次返回值，数量变化同样判为不一致", async (t) => {
  const { coordinator, invalidated, probes } = setup(t)
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: heads })
  t.mock.timers.tick(300)
  invalidated.length = 0
  coordinator.start()
  probes[0].resolve({ ...heads, conversationCount: 3 })
  await flush()
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["conversation-summary"]), 1)
})

test("探针在途时的再次请求只在结束后补读一次，早于已应用结果发起的读取被丢弃", async (t) => {
  const { coordinator, invalidated, probes } = setup(t)
  coordinator.start()
  void coordinator.probe()
  void coordinator.probe()
  assert.equal(probes.length, 1)

  // 在途探针返回前，重连问候已经带来更新的探针值。
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: heads })
  t.mock.timers.tick(300)
  invalidated.length = 0
  probes[0].resolve({ ...heads, conversationChecksum: "old" })
  await flush()
  assert.equal(probes.length, 2)
  probes[1].resolve(heads)
  await flush()
  t.mock.timers.tick(300)
  assert.deepEqual(invalidated, [])
})

test("探针失败交给错误入口，下一周期继续校验", async (t) => {
  const { coordinator, probes, failures } = setup(t)
  coordinator.start()
  const error = new Error("network")
  probes[0].reject(error)
  await flush()
  assert.deepEqual(failures, [error])
  t.mock.timers.tick(30_000)
  assert.equal(probes.length, 2)
})

test("销毁后丢弃待合并失效、在途探针结果和后续通知", async (t) => {
  const { coordinator, invalidated, probes, failures } = setup(t)
  coordinator.start()
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "direct", version: 1n })
  coordinator.dispose()
  probes[0].reject(new Error("late"))
  await flush()
  coordinator.receive({ type: "identity_profile_changed", version: 2n })
  void coordinator.probe()
  t.mock.timers.tick(60_000)
  assert.deepEqual(invalidated, [])
  assert.deepEqual(failures, [])
  assert.equal(probes.length, 1)
})

test("会话探针不一致、会话变更与本人会话状态变化都失效检索结果", async (t) => {
  const { coordinator, invalidated, probes } = setup(t)
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: heads })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["inbox-search"]), 1)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_changed", conversationId: "c1", conversationType: "direct", version: 2n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["inbox-search"]), 1)

  invalidated.length = 0
  coordinator.receive({ type: "conversation_state_changed", conversationId: "c1", version: 3n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["inbox-search"]), 1)

  invalidated.length = 0
  coordinator.start()
  probes[0].resolve({ ...heads, conversationChecksum: "12" })
  await flush()
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["inbox-search"]), 1)
})

test("探针与问候成功后在合并窗口结束时重试失败的同步读取，探针失败时不重试", async (t) => {
  const { coordinator, probes, retries } = setup(t)
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: heads })
  t.mock.timers.tick(300)
  assert.equal(retries.count, 1)

  coordinator.start()
  probes[0].resolve({ ...heads })
  await flush()
  assert.equal(retries.count, 1)
  t.mock.timers.tick(300)
  assert.equal(retries.count, 2)

  t.mock.timers.tick(30_000)
  probes[1].reject(new Error("network"))
  await flush()
  t.mock.timers.tick(300)
  assert.equal(retries.count, 2)
})

test("置顶顺序变更通知与探针差异都整区重读收件箱，不牵动会话内容", async (t) => {
  const { coordinator, invalidated, probes } = setup(t)
  coordinator.receive({ type: "server_hello", connectionId: "conn", syncHeads: heads })
  t.mock.timers.tick(300)
  invalidated.length = 0

  coordinator.receive({ type: "pin_order_changed", version: 5n })
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["inbox"]), 1)
  assert.equal(count(invalidated, ["inbox-attention"]), 1)
  assert.equal(count(invalidated, ["conversation-messages"]), 0)

  invalidated.length = 0
  coordinator.start()
  probes[0].resolve({ ...heads, pinOrderVersion: "6" })
  await flush()
  t.mock.timers.tick(300)
  assert.equal(count(invalidated, ["inbox"]), 1)
  assert.equal(count(invalidated, ["identity"]), 0)
  assert.equal(count(invalidated, ["conversation-summary"]), 0)
})
