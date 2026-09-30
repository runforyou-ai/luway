/** 验证服务端消息精度、连续分页的浏览边界与消息窗口的读取调度。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import {
  ConversationWindowController,
  compareConversationMessages,
  mergeConversationPage,
} from "../src/features/inbox/conversation-window.ts"
import type {
  ConversationMessageData,
  ConversationMessageListData,
} from "../src/api/conversations.ts"

/** 构造具有真实传输字段的消息。 */
function message(
  id: string,
  sequence: string,
  originatedAt = "2026-09-05T00:00:00Z",
): ConversationMessageData {
  return {
    id,
    messageSeq: sequence,
    originatedAt,
    sourceOrder: 0,
  } as ConversationMessageData
}

/** 构造带两个端点的连续页面。 */
function page(
  messages: ConversationMessageData[],
  hasEarlier = true,
  hasLater = true,
): ConversationMessageListData {
  return {
    messages,
    agentRuns: [],
    before: messages[0]?.id ?? null,
    after: messages[messages.length - 1]?.id ?? null,
    hasEarlier,
    hasLater,
  }
}

test("所有会话使用超过 JavaScript 安全整数的服务端序号", () => {
  const first = message("z", "9007199254740992", "2026-09-06T00:00:00Z")
  const second = message("a", "9007199254740993", "2026-09-05T00:00:00Z")
  assert.equal(compareConversationMessages(first, second), -1)
})

test("没有新消息的页面仍更新运行终态并保留既有消息", () => {
  const current = page([message("reply", "1")], false, false)
  const incoming = page([], false, false)
  incoming.agentRuns = [{
    id: "run", agentName: "AI 助手", status: "cancelled", errorCode: "session_closed", lastError: "session closed",
  }] as ConversationMessageListData["agentRuns"]
  const merged = mergeConversationPage(current, incoming, "after")
  assert.deepEqual(merged.agentRuns, incoming.agentRuns)
  assert.deepEqual(merged.messages, current.messages)
  assert.equal(merged.after, current.after)
})

test("消息排序忽略来源时间、来源编号和 UUID", () => {
  const first = message("z", "1", "2026-09-06T00:00:00Z")
  const second = message("a", "2", "2026-09-05T00:00:00Z")
  assert.equal(compareConversationMessages(first, second), -1)
  assert.equal(compareConversationMessages({ ...first, sourceOrder: 100 }, second), -1)
})

test("历史页前插与后续页追加各自保留另一端边界", () => {
  const current = page([message("b", "2"), message("c", "3")])
  const earlier = mergeConversationPage(
    current,
    page([message("a", "1")], false),
    "before",
  )
  assert.deepEqual(
    earlier.messages.map((row) => row.id),
    ["a", "b", "c"],
  )
  assert.deepEqual(
    [earlier.before, earlier.after, earlier.hasEarlier, earlier.hasLater],
    ["a", "c", false, true],
  )
  const later = mergeConversationPage(
    earlier,
    page([message("d", "4")], true, false),
    "after",
  )
  assert.deepEqual(
    [later.before, later.after, later.hasEarlier, later.hasLater],
    ["a", "d", false, false],
  )
})

test("空轮询不会清掉端点，重复页不会重复消息", () => {
  const current = page([message("b", "2"), message("c", "3")])
  const empty = mergeConversationPage(current, page([], false, false), "after")
  assert.deepEqual(
    [empty.before, empty.after, empty.hasEarlier, empty.hasLater],
    ["b", "c", true, false],
  )
  const duplicate = mergeConversationPage(
    empty,
    page([message("c", "3"), message("d", "4")], true, false),
    "after",
  )
  assert.deepEqual(
    duplicate.messages.map((row) => row.id),
    ["b", "c", "d"],
  )
})


test("失败消息沿用消息分页并在新一轮消息后保留", () => {
  const question = message("question", "1", "2026-09-05T00:00:00Z")
  const failure = { ...message("failure", "2", "2026-09-05T00:00:01Z"), type: "agent_error", body: "", sender: { sourceId: "agent", identityType: "agent", avatarUrl: "/avatar" } } as ConversationMessageData
  const first = mergeConversationPage(page([question], false, false), page([failure], false, false), "after")
  const next = mergeConversationPage(first, page([message("next", "3", "2026-09-05T00:00:02Z")], false, false), "after")
  assert.deepEqual(next.messages.map((row) => row.id), ["question", "failure", "next"])
  assert.equal(next.messages[1].sender?.avatarUrl, "/avatar")
  assert.deepEqual(mergeConversationPage(next, page([failure]), "before").messages, next.messages)
})

/** 等待已排队的微任务与回调执行完毕。 */
function flush() {
  return new Promise((resolve) => setImmediate(resolve))
}

/** 构造读取结果由测试逐个交付的窗口控制器，记录每次读取请求与位置保存次数；view.following 模拟贴底跟随。 */
function windowController() {
  const reads: { request: string; resolve: (page: ConversationMessageListData) => void; reject: (error: unknown) => void }[] = []
  const kept = { count: 0 }
  const view = { following: false }
  // 登记读取请求并返回由测试交付的结果。
  const request = (...parts: string[]) => {
    const deferred = Promise.withResolvers<ConversationMessageListData>()
    reads.push({ request: parts.join(":"), resolve: deferred.resolve, reject: deferred.reject })
    return deferred.promise
  }
  const controller = new ConversationWindowController({
    latest: () => request("latest"),
    context: (id) => request("context", id),
    page: (direction, cursor) => request(direction, cursor),
    window: (start, end) => request("window", start, end),
    keepPosition: () => {
      kept.count += 1
    },
    followingLatest: () => view.following,
  })
  return { controller, reads, kept, view }
}

/** 交付最近一次读取并等待控制器处理。 */
async function deliver(reads: ReturnType<typeof windowController>["reads"], expected: string, result: ConversationMessageListData) {
  await flush()
  const read = reads.at(-1)!
  assert.equal(read.request, expected)
  read.resolve(result)
  await flush()
}

/** 返回当前窗口的消息编号。 */
function ids(controller: ConversationWindowController) {
  return controller.getSnapshot().page?.messages.map((row) => row.id)
}

test("首次重读读取最新页，之后按首尾游标重读并替换窗口内容", async () => {
  const { controller, reads, kept } = windowController()
  const first = controller.refresh()
  await deliver(reads, "latest", page([message("a", "1"), message("b", "2")], false, false))
  assert.equal(await first, null)
  assert.deepEqual(ids(controller), ["a", "b"])
  assert.equal(kept.count, 0)
  void controller.refresh()
  // 窗口内 a 已删除，b 的正文发生变化。
  await deliver(reads, "window:a:b", page([{ ...message("b", "2"), body: "修改后" } as ConversationMessageData], false, false))
  assert.deepEqual(ids(controller), ["b"])
  assert.equal(controller.getSnapshot().page?.messages[0].body, "修改后")
  assert.equal(kept.count, 1)
})

test("最新模式重读后尾部仍有后续消息时连续补页到最新", async () => {
  const { controller, reads } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("a", "1"), message("b", "2")], false, false))
  void controller.refresh()
  await deliver(reads, "window:a:b", page([message("a", "1"), message("b", "2")], false, true))
  await deliver(reads, "after:b", page([message("c", "3")], true, true))
  await deliver(reads, "after:c", page([message("d", "4")], true, false))
  assert.deepEqual(ids(controller), ["a", "b", "c", "d"])
  assert.deepEqual([controller.getSnapshot().mode, controller.getSnapshot().page?.hasLater], ["latest", false])
})

test("锚点窗口重读只读取当前范围，尾端到达后回到最新模式", async () => {
  const { controller, reads } = windowController()
  const opened = controller.open("x")
  await deliver(reads, "context:x", page([message("w", "1"), message("x", "2"), message("y", "3")], true, true))
  assert.equal(await opened, true)
  assert.equal(controller.getSnapshot().mode, "anchor")
  void controller.refresh()
  await deliver(reads, "window:w:y", page([message("w", "1"), { ...message("x", "2"), body: "资料更新" } as ConversationMessageData, message("y", "3")], true, true))
  assert.equal(reads.length, 2)
  assert.equal(controller.getSnapshot().mode, "anchor")
  assert.equal(controller.getSnapshot().page?.messages[1].body, "资料更新")
  void controller.refresh()
  await deliver(reads, "window:w:y", page([message("w", "1"), message("x", "2"), message("y", "3")], true, false))
  assert.equal(controller.getSnapshot().mode, "latest")
})

test("补页在途时的多次重读合并为一次，并按补页后的边界读取", async () => {
  const { controller, reads } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("b", "2"), message("c", "3")], true, false))
  let positions = 0
  const loading = controller.loadPage("before", () => {
    positions += 1
  })
  const refreshes = [controller.refresh(), controller.refresh(), controller.refresh()]
  assert.equal(refreshes[0], refreshes[2])
  await deliver(reads, "before:b", page([message("a", "1")], false, true))
  await loading
  assert.equal(positions, 1)
  await deliver(reads, "window:a:c", page([message("a", "1"), message("b", "2"), message("c", "3")], false, false))
  await Promise.all(refreshes)
  assert.equal(reads.length, 3)
  assert.deepEqual(ids(controller), ["a", "b", "c"])
})

test("打开新窗口成功后丢弃早于它开始的重读结果与补页结果", async () => {
  const { controller, reads } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("m", "5"), message("n", "6")], true, false))
  const loading = controller.loadPage("before", () => undefined)
  await flush()
  const opened = controller.open("z")
  await deliver(reads, "context:z", page([message("y", "8"), message("z", "9")], true, true))
  assert.equal(await opened, true)
  reads[1].resolve(page([message("l", "4")], true, true))
  await loading
  assert.deepEqual(ids(controller), ["y", "z"])
  assert.equal(controller.getSnapshot().loadingDirection, null)
  const refreshing = controller.refresh()
  await deliver(reads, "window:y:z", page([message("y", "8"), message("z", "9")], true, true))
  const stale = controller.refresh()
  await flush()
  const reopened = controller.open()
  await deliver(reads, "latest", page([message("z", "9"), message("zz", "10")], true, false))
  reads[4].resolve(page([message("y", "8")], true, true))
  await Promise.all([refreshing, stale, reopened])
  assert.deepEqual(ids(controller), ["z", "zz"])
})

test("取消定位后仍合入定位期间完成的重读结果", async () => {
  const { controller, reads } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("a", "1"), message("b", "2")], false, false))
  const refreshing = controller.refresh()
  await flush()
  const opened = controller.open("q")
  await flush()
  controller.cancel()
  reads[1].resolve(page([message("a", "1"), { ...message("b", "2"), body: "新正文" } as ConversationMessageData], false, false))
  await refreshing
  assert.equal(controller.getSnapshot().page?.messages[1].body, "新正文")
  reads[2].resolve(page([message("q", "9")], true, true))
  assert.equal(await opened, false)
  assert.deepEqual(ids(controller), ["a", "b"])
})

test("重读结果与当前窗口一致时保留原窗口且不保存阅读位置", async () => {
  const { controller, reads, kept } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("a", "1")], false, false))
  const current = controller.getSnapshot().page
  void controller.refresh()
  await deliver(reads, "window:a:a", page([message("a", "1")], false, false))
  assert.equal(controller.getSnapshot().page, current)
  assert.equal(kept.count, 0)
})

test("重读只替换内容变化的消息，其余消息沿用原对象", async () => {
  const { controller, reads } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("a", "1"), message("b", "2")], false, false))
  const [a] = controller.getSnapshot().page!.messages
  void controller.refresh()
  await deliver(reads, "window:a:b", page([message("a", "1"), { ...message("b", "2"), body: "修改后" } as ConversationMessageData], false, false))
  const messages = controller.getSnapshot().page!.messages
  assert.equal(messages[0], a)
  assert.equal(messages[1].body, "修改后")
})

test("贴底跟随最新消息时重读最新页，窗口收缩到最新一页", async () => {
  const { controller, reads, view } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("c", "3"), message("d", "4")], true, false))
  void controller.loadPage("before", () => {})
  await deliver(reads, "before:c", page([message("a", "1"), message("b", "2")], false, true))
  assert.deepEqual(ids(controller), ["a", "b", "c", "d"])
  view.following = true
  void controller.refresh()
  await deliver(reads, "latest", page([message("d", "4"), message("e", "5")], true, false))
  assert.deepEqual(ids(controller), ["d", "e"])
  assert.equal(controller.getSnapshot().page?.hasEarlier, true)
})

test("贴底重读期间离开底部时丢弃最新页，按原首尾游标重读并保留历史", async () => {
  const { controller, reads, view } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("c", "3"), message("d", "4")], true, false))
  void controller.loadPage("before", () => {})
  await deliver(reads, "before:c", page([message("a", "1"), message("b", "2")], false, true))
  view.following = true
  void controller.refresh()
  await flush()
  view.following = false
  await deliver(reads, "latest", page([message("d", "4"), message("e", "5")], true, false))
  await deliver(reads, "window:a:d", page([message("a", "1"), message("b", "2"), message("c", "3"), message("d", "4")], false, true))
  await deliver(reads, "after:d", page([message("e", "5")], true, false))
  assert.deepEqual(ids(controller), ["a", "b", "c", "d", "e"])
})

test("重读失败时抛出错误并保留已展示内容，下次重读恢复", async () => {
  const { controller, reads } = windowController()
  void controller.refresh()
  await deliver(reads, "latest", page([message("a", "1")], false, false))
  const failed = controller.refresh()
  await flush()
  reads[1].reject(new Error("offline"))
  await assert.rejects(failed, /offline/)
  assert.deepEqual(ids(controller), ["a"])
  void controller.refresh()
  await deliver(reads, "window:a:a", page([message("a", "1"), message("b", "2")], false, false))
  assert.deepEqual(ids(controller), ["a", "b"])
})
