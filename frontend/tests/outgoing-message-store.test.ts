/** 验证发送状态在响应、查询窗口和会话切换下的收尾顺序。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import { stripTypeScriptTypes } from "node:module"

const storeSource = readFileSync(
  new URL("../src/lib/outgoing-message-store.ts", import.meta.url),
  "utf8",
)
const storeCode =
  stripTypeScriptTypes(storeSource).replace(/^export /gm, "") +
  "\nexports.OutgoingMessageStore = OutgoingMessageStore;" +
  "\nexports.conversationSendingIndicatorDelay = conversationSendingIndicatorDelay;" +
  "\nexports.windowCoverage = windowCoverage;" +
  "\nexports.coveredByWindow = coveredByWindow;"
const exported: Record<string, any> = {}
runInNewContext(storeCode, {
  exports: exported,
  setTimeout,
  clearTimeout,
  Map,
  Set,
})
const {
  OutgoingMessageStore,
  conversationSendingIndicatorDelay,
  windowCoverage,
  coveredByWindow,
} = exported

/** 构造一条最小发送草稿。 */
function draft(clientMessageID: string, body = "你好") {
  return {
    clientMessageID,
    body,
    originatedAt: "2026-09-11T00:00:00.000Z",
    replyTo: null,
    mentions: [],
    mentionAll: false,
    mentionAllToken: null,
  }
}

/** 构造一条查询窗口消息。 */
function saved(id: string, clientMessageId: string | null) {
  return { id, clientMessageId, body: "你好" }
}

/** 读取指定会话的发送项。 */
function thread(store: any, conversationID: string) {
  return store.snapshot().get(conversationID) ?? []
}

test("查询先到与响应先到都只保留一条消息", () => {
  const queryFirst = new OutgoingMessageStore()
  queryFirst.start("c1", draft("m1"))
  queryFirst.reconcile("c1", [saved("s1", "m1")])
  assert.equal(thread(queryFirst, "c1").length, 0)
  // 命令响应迟到时按编号找不到发送项，快照保持为空。
  queryFirst.succeed("m1", saved("s1", "m1"))
  assert.equal(thread(queryFirst, "c1").length, 0)
  queryFirst.dispose()

  const responseFirst = new OutgoingMessageStore()
  responseFirst.start("c1", draft("m1"))
  responseFirst.succeed("m1", saved("s1", "m1"))
  assert.equal(thread(responseFirst, "c1")[0].status, "sent")
  responseFirst.reconcile("c1", [saved("s1", "m1")])
  assert.equal(thread(responseFirst, "c1").length, 0)
  responseFirst.dispose()
})

test("服务端已提交但客户端超时的失败项由查询窗口收尾", () => {
  const store = new OutgoingMessageStore()
  store.start("c1", draft("m1"))
  store.fail("m1")
  assert.equal(thread(store, "c1")[0].status, "failed")
  store.reconcile("c1", [saved("s1", "m1")])
  assert.equal(thread(store, "c1").length, 0)
  store.dispose()
})

test("窗口只收尾本人关联的消息，他人消息保留原发送状态", () => {
  const store = new OutgoingMessageStore()
  store.start("c1", draft("m1"))
  store.reconcile("c1", [saved("s9", null), saved("s8", "other")])
  assert.equal(thread(store, "c1").length, 1)
  store.dispose()
})

test("切换会话后迟到的响应留在原会话", () => {
  const store = new OutgoingMessageStore()
  store.start("c1", draft("m1"))
  store.start("c2", draft("m2"))
  store.succeed("m1", saved("s1", "m1"))
  assert.equal(thread(store, "c1")[0].status, "sent")
  assert.equal(thread(store, "c2").length, 1)
  assert.equal(thread(store, "c2")[0].status, "sending")
  // 窗口收尾只作用于自身会话。
  store.reconcile("c2", [saved("s1", "m1")])
  assert.equal(thread(store, "c1").length, 1)
  store.dispose()
})

test("草稿转正后发送状态移交正式会话", () => {
  const store = new OutgoingMessageStore()
  store.start("draft:u1", draft("m1"))
  store.adopt("draft:u1", "c1")
  assert.equal(thread(store, "draft:u1").length, 0)
  assert.equal(thread(store, "c1").length, 1)
  store.succeed("m1", saved("s1", "m1"))
  assert.equal(thread(store, "c1")[0].status, "sent")
  store.dispose()
})

test("失权清理只移除该会话的发送项", () => {
  const store = new OutgoingMessageStore()
  store.start("c1", draft("m1"))
  store.start("c2", draft("m2"))
  store.forgetConversation("c1")
  assert.equal(thread(store, "c1").length, 0)
  assert.equal(thread(store, "c2").length, 1)
  store.dispose()
})

test("发送中提示延迟出现，收尾时释放对应计时器", async () => {
  const store = new OutgoingMessageStore()
  const changes: number[] = []
  store.subscribe(() => changes.push(thread(store, "c1").length))
  store.start("c1", draft("m1"))
  assert.equal(thread(store, "c1")[0].showSending, false)
  await new Promise((resolve) =>
    setTimeout(resolve, conversationSendingIndicatorDelay + 50),
  )
  assert.equal(thread(store, "c1")[0].showSending, true)

  store.start("c1", draft("m2"))
  store.reconcile("c1", [saved("s2", "m2")])
  const settled = changes.length
  await new Promise((resolve) =>
    setTimeout(resolve, conversationSendingIndicatorDelay + 50),
  )
  assert.equal(changes.length, settled)
  store.dispose()
})

test("窗口覆盖判定同时认本人逻辑编号和已确认的服务端编号", () => {
  const coverage = windowCoverage([saved("s1", "m1"), saved("s2", null)])
  const pending = { ...draft("m1"), status: "sending", showSending: false, saved: null }
  const confirmed = { ...draft("m3"), status: "sent", showSending: false, saved: saved("s2", null) }
  const outside = { ...draft("m9"), status: "sending", showSending: false, saved: null }
  assert.equal(coveredByWindow(pending, coverage), true)
  assert.equal(coveredByWindow(confirmed, coverage), true)
  assert.equal(coveredByWindow(outside, coverage), false)
})

test("丢弃只移除指定发送项，迟到的结果不写回", () => {
  const store = new OutgoingMessageStore()
  store.start("c1", draft("m1"))
  store.start("c1", draft("m2"))
  store.discard("m1")
  assert.equal(
    thread(store, "c1").map((item: any) => item.clientMessageID).join(","),
    "m2",
  )
  store.succeed("m1", saved("s1", "m1"))
  assert.equal(thread(store, "c1").length, 1)
  store.discard("m2")
  assert.equal(store.snapshot().has("c1"), false)
  store.dispose()
})
