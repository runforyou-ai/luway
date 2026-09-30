/** 用可控响应顺序验证分页、窗口刷新与查询代次隔离。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { InboxListController, type InboxListPorts } from "../src/features/inbox/inbox-list-controller.ts"
import type { InboxConversationData, InboxQuery } from "../src/api/index.ts"

/** 构造保持服务端精度的排序位置。 */
function row(number: number): InboxConversationData {
  return { id: String(number), positionCursor: `p${number}`, lastActivityAt: `2026-09-09T00:00:00.${String(1000 - number).padStart(6, "0")}Z` } as InboxConversationData
}

/** 构造具有双向边界的权威窗口。 */
function windowPage(start: number, end: number) {
  return {
    conversations: Array.from({ length: Math.max(0, end - start + 1) }, (_, i) => row(start + i)),
    startCursor: `p${start}`, endCursor: `p${end}`, hasBefore: start > 1, hasAfter: end < 220,
  }
}

/** 创建可替换响应的真实控制器与读取适配器。 */
function fixture(locateId: string | null = null) {
  const trace: string[] = []
  const records = new Map(Array.from({ length: 220 }, (_, i) => [String(i + 1), row(i + 1)]))
  let top = true
  let interacting = false
  let restored: Parameters<InboxListPorts["restore"]> | undefined
  const unavailable: string[] = []
  const ports: InboxListPorts = {
    page: async (cursor = "", before = "") => {
      trace.push(`page:${cursor}:${before}`)
      const start = cursor ? Number(cursor.slice(1)) + 1 : before ? Math.max(1, Number(before.slice(1)) - 50) : 1
      const window = windowPage(start, before ? Number(before.slice(1)) - 1 : Math.min(start + 49, 220))
      return { ...window, hasMore: window.hasAfter, nextCursor: window.endCursor, unreadCount: 99, attentionUnreadCount: 88, pendingCount: 0 }
    },
    window: async (start, end) => { trace.push(`window:${start}:${end}`); return windowPage(Number(start.slice(1)), Number(end.slice(1))) },
    context: async () => { trace.push("context"); return windowPage(81, 130) },
    rows: async (ids) => ({ results: ids.map((id) => ({ id, availability: records.has(id) ? "matching" : "unavailable", conversation: records.get(id) ?? null })) }) as Awaited<ReturnType<InboxListPorts["rows"]>>,
    capture: () => ({ id: "80", cursor: "p80", width: 390, height: 844, neighbors: [{ id: "80", offset: -10 }, { id: "81", offset: 58 }, { id: "79", offset: -78 }] }),
    atTop: () => top,
    interacting: () => interacting,
    restore: (...args) => { restored = args },
    unavailable: (ids) => { unavailable.push(...ids) },
  }
  const controller = new InboxListController(ports, { scope: "chat" } as InboxQuery, { locateId })
  return { controller, ports, trace, records, unavailable, top: (value: boolean) => { top = value }, interact: (value: boolean) => { interacting = value }, restored: () => restored }
}

test("补页在途时合并重复触底，刷新等待完整扩展区间且不丢页", async () => {
  const f = fixture()
  const initialPage = f.ports.page
  let initial = true
  f.ports.page = (cursor, before) => {
    if (!cursor && !before && initial) { initial = false; return initialPage("p50") }
    return initialPage(cursor, before)
  }
  await f.controller.request("initial")
  f.top(false)
  await f.controller.request("after")
  const gate = Promise.withResolvers<Awaited<ReturnType<InboxListPorts["page"]>>>()
  const original = f.ports.page
  f.ports.page = (cursor, before) => cursor === "p150" ? gate.promise : original(cursor, before)
  const pending = f.controller.request("after")
  void f.controller.request("after")
  void f.controller.request("refresh")
  void f.controller.request("refresh")
  assert.equal(f.controller.getSnapshot().status, "loadingMore")
  gate.resolve(await original("p150"))
  await pending
  assert.equal(f.trace.filter((call) => call === "page:p150:").length, 1)
  assert.equal(f.trace.filter((call) => call === "window:p51:p200").length, 2)
  assert.equal(f.controller.getSnapshot().ids.length, 150)
  assert.equal(f.controller.getSnapshot().endCursor, "p200")
})

test("深处轮询自动应用上浮，锚定原邻居且保留完整窗口", async () => {
  const f = fixture()
  await f.controller.request("initial")
  await f.controller.request("after")
  f.top(false)
  f.records.set("80", { ...row(80), lastActivityAt: "2026-09-10T00:00:00.000001Z" })
  f.ports.window = async () => ({ ...windowPage(1, 100), conversations: windowPage(1, 100).conversations.filter((row) => row.id !== "80") })
  await f.controller.request("poll")
  assert.equal(f.controller.getSnapshot().ids[79], "81")
  assert.ok(f.restored()![1].has("80"))
  assert.equal(f.restored()![2], false)
  assert.equal(f.controller.getSnapshot().ids.length, 99)
})

test("补页和刷新失败保留已确认内容与边界，重试相同范围", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  const base = f.controller.getSnapshot()
  const page = f.ports.page
  f.ports.page = async () => { throw new Error("offline") }
  await f.controller.request("after")
  assert.deepEqual(f.controller.getSnapshot().ids, base.ids)
  assert.equal(f.controller.getSnapshot().endCursor, base.endCursor)
  assert.equal(f.controller.getSnapshot().error, "after")
  f.ports.page = page
  await f.controller.request("after")
  assert.equal(f.controller.getSnapshot().ids.length, 100)
  const read = f.ports.window
  f.ports.window = async () => { throw new Error("offline") }
  await f.controller.request("refresh")
  assert.equal(f.controller.getSnapshot().error, "refresh")
  assert.equal(f.controller.getSnapshot().ids.length, 100)
  f.ports.window = read
  await f.controller.request("refresh")
  assert.equal(f.trace.at(-1), "window:p1:p100")
  assert.equal(f.controller.getSnapshot().error, null)
})

test("刷新在途的补页排队，旧查询结果不写入已失效的控制器", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  const gate = Promise.withResolvers<ReturnType<typeof windowPage>>()
  const originalWindow = f.ports.window
  f.ports.window = (start, end) => end === "p50" ? gate.promise : originalWindow(start, end)
  const pending = f.controller.request("refresh")
  void f.controller.request("after")
  gate.resolve(windowPage(1, 50))
  await pending
  assert.equal(f.controller.getSnapshot().endCursor, "p100")
  const old = Promise.withResolvers<ReturnType<typeof windowPage>>()
  f.ports.window = () => old.promise
  const obsolete = f.controller.request("refresh")
  f.controller.dispose()
  old.resolve(windowPage(1, 200))
  await obsolete
  assert.equal(f.controller.getSnapshot().endCursor, "p100")
})

test("空区间恢复原邻域，失权立即移除并清理详情", async () => {
  const f = fixture()
  await f.controller.request("initial")
  await f.controller.request("after")
  f.top(false)
  f.records.delete("80")
  f.ports.window = async () => ({ ...windowPage(1, 100), conversations: [] })
  await f.controller.request("refresh")
  assert.ok(f.trace.includes("context"))
  assert.deepEqual(f.unavailable, ["80"])
  assert.equal(f.controller.getSnapshot().ids[0], "81")
  assert.equal(f.controller.getSnapshot().hasBefore, true)
})

test("切换查询后残留的锚点不参与空区间恢复", async () => {
  const f = fixture()
  // 新查询首屏为空，视口仍持有上一个查询滚动到的第 80 条锚点。
  f.ports.page = async () => ({ ...windowPage(1, 0), startCursor: "", endCursor: "", hasBefore: false, hasAfter: false, hasMore: false, nextCursor: "", unreadCount: 0, attentionUnreadCount: 0, pendingCount: 0 })
  await f.controller.request("initial")
  await f.controller.request("refresh")
  assert.equal(f.trace.includes("context"), false)
  assert.equal(f.controller.getSnapshot().ids.length, 0)
  assert.equal(f.controller.getSnapshot().error, null)
})

test("带选中会话进入列表时读取该会话邻域并定位到它", async () => {
  const f = fixture("100")
  await f.controller.request("initial")
  assert.ok(f.trace.includes("context"))
  assert.equal(f.trace.includes("page::"), false)
  assert.equal(f.controller.getSnapshot().ids[0], "81")
  assert.equal(f.restored()![0]!.id, "100")
  assert.deepEqual(f.restored()![0]!.neighbors, [{ id: "100", offset: 0 }])
  assert.equal(f.restored()![1].has("100"), false)
  assert.equal(f.restored()![2], false)
})

test("选中会话不属于当前筛选时读取首页并回到顶部", async () => {
  const f = fixture("100")
  f.ports.context = async () => { f.trace.push("context"); return { conversations: [], startCursor: "", endCursor: "", hasBefore: false, hasAfter: false } }
  await f.controller.request("initial")
  assert.ok(f.trace.includes("context"))
  assert.ok(f.trace.includes("page::"))
  assert.equal(f.controller.getSnapshot().ids[0], "1")
  assert.equal(f.restored()![0], null)
  assert.equal(f.restored()![2], true)
})

test("带原位置的书签锚点读到空邻域时保留空窗口，不回落首页", async () => {
  const anchor = { id: "80", cursor: "p80", width: 390, height: 844, neighbors: [{ id: "80", offset: -10 }] }
  const f = fixture()
  const bookmark = { state: { ...f.controller.getSnapshot() }, anchor }
  const controller = new InboxListController(f.ports, { scope: "chat" } as InboxQuery, { bookmark })
  f.ports.context = async () => { f.trace.push("context"); return { conversations: [], startCursor: "", endCursor: "", hasBefore: true, hasAfter: true } }
  await controller.request("initial")
  assert.ok(f.trace.includes("context"))
  assert.equal(f.trace.includes("page::"), false)
  assert.equal(controller.getSnapshot().ids.length, 0)
  assert.equal(controller.getSnapshot().hasBefore, true)
})

test("读取期间用户离开顶部或操作菜单时不自动回顶重排", async () => {
  const f = fixture()
  await f.controller.request("initial")
  const read = f.ports.rows
  f.ports.rows = async (ids) => { f.top(false); return read(ids) }
  await f.controller.request("poll")
  assert.equal(f.restored()![2], false)
  f.interact(true)
  await f.controller.request("refresh")
  assert.equal(f.restored()![2], false)
  f.interact(false)
  f.controller.settle()
  f.top(true)
  f.ports.rows = read
  await f.controller.request("refresh")
  assert.equal(f.restored()![2], true)
})

test("独立详情先失权时仅移除该行，并阻止旧窗口读取恢复它", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  const gate = Promise.withResolvers<ReturnType<typeof windowPage>>()
  const read = f.ports.window
  const originalWindow = f.ports.window
  f.ports.window = (start, end) => end === "p50" ? gate.promise : originalWindow(start, end)
  const pending = f.controller.request("refresh")
  f.records.delete("20")
  f.controller.removeUnavailable("20")
  assert.equal(f.controller.getSnapshot().ids.length, 49)
  assert.ok(!f.controller.getSnapshot().ids.includes("20"))
  f.ports.window = read
  gate.resolve(windowPage(1, 50))
  await pending
  assert.ok(!f.controller.getSnapshot().ids.includes("20"))
  assert.equal(f.controller.getSnapshot().endCursor, "p50")
  assert.deepEqual(f.controller.getSnapshot().unavailableIds, ["20"])
})

test("批量资格读取失败不接纳半个页面", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  await f.controller.request("after")
  const rows = f.ports.rows
  f.ports.rows = async () => { throw new Error("rows offline") }
  await f.controller.request("after")
  assert.equal(f.controller.getSnapshot().ids.length, 100)
  assert.equal(f.controller.getSnapshot().endCursor, "p100")
  f.ports.rows = rows
  await f.controller.request("after")
  assert.equal(f.controller.getSnapshot().ids.length, 150)
})

test("顶部新增超过一页时读取连续扩展范围，不遗漏首页与旧区间之间的行", async () => {
  const f = fixture()
  await f.controller.request("initial")
  const original = f.ports.page
  f.ports.page = async () => ({ ...await original(), startCursor: "new-start" })
  f.ports.window = async (start, end) => {
    f.trace.push(`window:${start}:${end}`)
    return { ...windowPage(1, start === "new-start" ? 120 : 50), hasBefore: start !== "new-start" }
  }
  await f.controller.request("poll")
  assert.deepEqual(f.trace.filter((call) => call.startsWith("window:")), ["window:p1:p50", "window:new-start:p50"])
  assert.equal(f.controller.getSnapshot().ids.length, 120)
})

test("轮询失败后通过重试恢复原窗口，并继续后续轮询", async () => {
  const f = fixture()
  await f.controller.request("initial")
  await f.controller.request("after")
  f.top(false)
  const read = f.ports.window
  f.ports.window = async () => { throw new Error("poll offline") }
  await f.controller.request("poll")
  assert.equal(f.controller.getSnapshot().error, "poll")
  assert.equal(f.controller.getSnapshot().ids.length, 100)
  f.ports.window = read
  await f.controller.retry()
  assert.equal(f.controller.getSnapshot().error, null)
  assert.equal(f.controller.getSnapshot().endCursor, "p100")
  assert.equal(f.restored()![2], false)
  const calls = f.trace.length
  await f.controller.request("poll")
  assert.ok(f.trace.length > calls)
  assert.equal(f.controller.getSnapshot().ids.length, 100)
})

test("排队与合并请求返回的 Promise 等待整轮实际读取完成", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  const gate = Promise.withResolvers<ReturnType<typeof windowPage>>()
  const originalWindow = f.ports.window
  f.ports.window = (start, end) => end === "p50" ? gate.promise : originalWindow(start, end)
  const refreshing = f.controller.request("refresh")
  const more = f.controller.request("after")
  const duplicate = f.controller.request("after")
  let completed = false
  void more.then(() => { completed = true })
  await Promise.resolve()
  assert.equal(completed, false)
  gate.resolve(windowPage(1, 50))
  await Promise.all([refreshing, more, duplicate])
  assert.equal(completed, true)
  assert.equal(f.controller.getSnapshot().ids.length, 100)
})

test("按住第80条时摘要先更新，松手后自动上浮且原81条作为补偿候选", async () => {
  const f = fixture()
  await f.controller.request("initial")
  await f.controller.request("after")
  f.top(false)
  f.interact(true)
  const moved = { ...row(80), lastActivityAt: "2026-09-10T00:00:00Z", positionCursor: "new80" }
  f.records.set("80", moved)
  f.ports.window = async () => ({ ...windowPage(1, 100), conversations: [moved, ...windowPage(1, 100).conversations.filter((row) => row.id !== "80")] })
  await f.controller.request("poll")
  assert.equal(f.controller.getSnapshot().ids[79], "80")
  f.controller.settle()
  assert.equal(f.controller.getSnapshot().ids[79], "80")
  f.interact(false)
  f.controller.settle()
  assert.equal(f.controller.getSnapshot().ids[0], "80")
  assert.equal(f.controller.getSnapshot().ids.filter((id) => id === "80").length, 1)
  assert.ok(f.restored()![1].has("80"))
  assert.equal(f.restored()![0]!.neighbors[1].id, "81")
  assert.equal(f.restored()![2], false)
  assert.equal(f.controller.getSnapshot().operation, null)
  const revision = f.controller.getSnapshot().revision
  f.controller.settle()
  assert.equal(f.controller.getSnapshot().revision, revision)
})

test("操作期间连续补页追加到尾部，不打断持续向下浏览", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  f.interact(true)
  await f.controller.request("after")
  await f.controller.request("after")
  assert.equal(f.controller.getSnapshot().ids.length, 150)
  assert.ok(f.trace.includes("page:p100:"))
  f.interact(false)
  f.controller.settle()
  assert.equal(f.controller.getSnapshot().ids.length, 150)
  assert.equal(f.controller.getSnapshot().endCursor, "p150")
})

test("深处新增超过一页自动扩展到最新顶部，不丢原浏览范围", async () => {
  const f = fixture()
  await f.controller.request("initial")
  await f.controller.request("after")
  f.top(false)
  const original = f.ports.page
  f.ports.page = async () => ({ ...await original(), startCursor: "new-start" })
  f.ports.window = async (start, end) => {
    f.trace.push(`window:${start}:${end}`)
    return { ...windowPage(1, start === "new-start" ? 180 : 100), hasBefore: start !== "new-start" }
  }
  await f.controller.request("poll")
  assert.equal(f.controller.getSnapshot().ids.length, 180)
  assert.equal(f.controller.getSnapshot().hasBefore, false)
  assert.equal(f.restored()![2], false)
})

test("返回时缓存保留则重读原完整窗口，缓存缺失则直接定位原邻域", async () => {
  const f = fixture()
  await f.controller.request("initial")
  await f.controller.request("after")
  await f.controller.request("after")
  f.top(false)
  const bookmark = f.controller.remember()
  const query = { scope: "chat" } as InboxQuery
  const cached = new InboxListController(f.ports, query, { bookmark, cached: true })
  assert.equal(cached.getSnapshot().ids.length, 150)
  await cached.request("initial")
  assert.equal(cached.getSnapshot().ids.length, 150)
  assert.ok(f.trace.includes("window:p1:p150"))
  f.trace.length = 0
  const evicted = new InboxListController(f.ports, query, { bookmark })
  await evicted.request("initial")
  assert.deepEqual(f.trace, ["context"])
  assert.equal(evicted.getSnapshot().ids[0], "81")
  assert.equal(f.restored()![0]!.id, "80")
  assert.equal(f.restored()![2], false)
})

test("操作中失权立即移除，待应用顺序不能让该行复活", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.interact(true)
  f.records.delete("20")
  await f.controller.request("poll")
  assert.ok(!f.controller.getSnapshot().ids.includes("20"))
  f.interact(false)
  f.controller.settle()
  assert.ok(!f.controller.getSnapshot().ids.includes("20"))
  assert.deepEqual(f.unavailable, ["20"])
})

test("补页读取期间上浮的未加载会话只能由随后的变更通知发现", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  f.trace.length = 0
  // 第 150 条在补页读取发出之后才上浮到首位，本轮补页的窗口仍是发起时的边界。
  let surfaced = false
  const moved = { ...row(150), positionCursor: "top", lastActivityAt: "2026-09-10T00:00:00.000001Z" }
  f.ports.page = async (cursor = "", before = "") => {
    f.trace.push(`page:${cursor}:${before}`)
    if (cursor === "p50") return { ...windowPage(51, 100), hasMore: true, nextCursor: "p100", unreadCount: 99, attentionUnreadCount: 88, pendingCount: 0 }
    const base = windowPage(1, 50)
    const page = surfaced ? { ...base, conversations: [moved, ...base.conversations], startCursor: "top" } : base
    return { ...page, hasMore: true, nextCursor: page.endCursor, unreadCount: 99, attentionUnreadCount: 88, pendingCount: 0 }
  }
  const gate = Promise.withResolvers<ReturnType<typeof windowPage>>()
  f.ports.window = async (start, end) => {
    f.trace.push(`window:${start}:${end}`)
    if (start === "top") {
      const base = windowPage(1, Number(end.slice(1)))
      return { ...base, conversations: [moved, ...base.conversations], startCursor: "top", hasBefore: false }
    }
    return start === "p1" && end === "p100" && !f.trace.includes("page::") ? gate.promise : { ...windowPage(1, Number(end.slice(1))), hasBefore: surfaced }
  }
  const paging = f.controller.request("after")
  await Promise.resolve()
  surfaced = true
  const notified = f.controller.request("poll")
  gate.resolve({ ...windowPage(1, 100), hasBefore: false })
  await Promise.all([paging, notified])
  // 补页读到的窗口不含上浮会话，变更通知触发的重读把窗口扩展到最新首页。
  assert.deepEqual(f.trace.filter((call) => call.startsWith("window:")), ["window:p1:p100", "window:p1:p100", "window:top:p100"])
  assert.equal(f.controller.getSnapshot().ids[0], "150")
  assert.equal(f.controller.getSnapshot().ids.filter((id) => id === "150").length, 1)
  assert.equal(f.controller.getSnapshot().ids.length, 101)
  assert.equal(f.controller.getSnapshot().hasBefore, false)
})

test("尚未开始的重读合并重复通知，在途读取不吞掉新到达的通知", async () => {
  const f = fixture()
  await f.controller.request("initial")
  f.top(false)
  f.trace.length = 0
  const gate = Promise.withResolvers<Awaited<ReturnType<InboxListPorts["page"]>>>()
  const originalPage = f.ports.page
  let gated = true
  f.ports.page = (cursor, before) => {
    if (!gated || cursor || before) return originalPage(cursor, before)
    gated = false
    f.trace.push("page::")
    return gate.promise
  }
  const first = f.controller.request("poll")
  void f.controller.request("poll")
  void f.controller.request("poll")
  gate.resolve({ ...windowPage(1, 50), hasMore: true, nextCursor: "p50", unreadCount: 99, attentionUnreadCount: 88, pendingCount: 0 })
  await first
  // 在途读取之后只补读一次，其余重复通知合并到该次读取。
  assert.equal(f.trace.filter((call) => call === "page::").length, 2)
  assert.equal(f.controller.getSnapshot().ids.length, 50)
})

test("读取失败后到达的变更通知继续重读并恢复窗口", async () => {
  const f = fixture()
  await f.controller.request("initial")
  await f.controller.request("after")
  f.top(false)
  const read = f.ports.window
  f.ports.window = async () => { throw new Error("offline") }
  await f.controller.request("poll")
  assert.equal(f.controller.getSnapshot().error, "poll")
  f.ports.window = read
  await f.controller.request("poll")
  assert.equal(f.controller.getSnapshot().error, null)
  assert.equal(f.controller.getSnapshot().ids.length, 100)
  assert.equal(f.controller.getSnapshot().endCursor, "p100")
})

test("列表加载到尾端后新增的会话仍可经变更通知发现", async () => {
  const f = fixture()
  let added = false
  const created = { ...row(1), id: "300", positionCursor: "top", lastActivityAt: "2026-09-10T00:00:00.000001Z" }
  const loaded = () => ({ ...windowPage(1, 50), hasBefore: false, hasAfter: false })
  f.ports.page = async () => {
    f.trace.push("page::")
    const base = loaded()
    const page = added ? { ...base, conversations: [created, ...base.conversations], startCursor: "top" } : base
    return { ...page, hasMore: false, nextCursor: page.endCursor, unreadCount: 0, attentionUnreadCount: 0, pendingCount: 0 }
  }
  f.ports.window = async (start, end) => {
    f.trace.push(`window:${start}:${end}`)
    const base = loaded()
    if (start === "top") return { ...base, conversations: [created, ...base.conversations], startCursor: "top" }
    return { ...base, hasBefore: added }
  }
  await f.controller.request("initial")
  assert.equal(f.controller.getSnapshot().hasAfter, false)
  assert.equal(f.controller.getSnapshot().ids.length, 50)
  added = true
  f.records.set("300", created)
  await f.controller.request("poll")
  const state = f.controller.getSnapshot()
  assert.equal(state.ids[0], "300")
  assert.equal(state.ids.filter((id) => id === "300").length, 1)
  assert.equal(state.ids.length, 51)
  assert.equal(state.hasBefore, false)
  assert.deepEqual(f.trace.filter((call) => call.startsWith("window:")), ["window:p1:p50", "window:top:p50"])
})

/** 创建按置顶顺序整区读取的控制器，pages 给出每次首页读取对应的分页序列。 */
function pinnedFixture(rounds: { version: string; pages: number[][]; failAfterFirst?: boolean; laterVersion?: string }[]) {
  const trace: string[] = []
  let round = -1
  let interacting = false
  let restored: Parameters<InboxListPorts["restore"]> | undefined
  const ports = {
    page: async (cursor = "") => {
      if (!cursor) round = Math.min(round + 1, rounds.length - 1)
      const current = rounds[round]
      const index = cursor ? Number(cursor.split(":")[1]) + 1 : 0
      trace.push(`page:${current.version}:${index}`)
      if (index > 0 && current.failAfterFirst) throw new Error("inbox_cursor_invalid")
      // 续页读取期间顺序已变化时，续页携带变化后的顺序版本。
      const version = index > 0 && current.laterVersion ? current.laterVersion : current.version
      return {
        conversations: (current.pages[index] ?? []).map(row), startCursor: `${current.version}:0`, endCursor: `${current.version}:${index}`,
        hasBefore: false, hasMore: index < current.pages.length - 1, pinOrderVersion: version, attentionUnreadCount: 0, pendingCount: 0,
      }
    },
    window: async () => { throw new Error("置顶区不按游标区间重读") },
    context: async () => { throw new Error("置顶区不读取锚点上下文") },
    rows: async (ids: string[]) => ({ results: ids.map((id) => ({ id, availability: "matching", conversation: row(Number(id)) })) }),
    capture: () => ({ id: "2", cursor: "", width: 390, height: 844, neighbors: [{ id: "2", offset: 0 }, { id: "3", offset: 68 }] }),
    atTop: () => false,
    interacting: () => interacting,
    restore: (...args: Parameters<InboxListPorts["restore"]>) => { restored = args },
    unavailable: () => {},
  } as unknown as InboxListPorts
  const controller = new InboxListController(ports, { partition: "pinned", scope: "chat" } as InboxQuery, { region: true })
  return { controller, trace, restored: () => restored, interact: (value: boolean) => { interacting = value } }
}

test("置顶区按同一顺序版本读完全部分页，不保留分页边界", async () => {
  const f = pinnedFixture([{ version: "7", pages: [[1, 2], [3, 4], [5]] }])
  await f.controller.request("initial")
  const state = f.controller.getSnapshot()
  assert.deepEqual(state.ids, ["1", "2", "3", "4", "5"])
  assert.equal(state.pinOrderVersion, "7")
  assert.equal(state.hasBefore, false)
  assert.equal(state.hasAfter, false)
  assert.deepEqual(f.trace, ["page:7:0", "page:7:1", "page:7:2"])
})

test("置顶区续页游标失效时舍弃本轮，从新版本首页整区重读", async () => {
  const f = pinnedFixture([
    { version: "7", pages: [[1, 2], [3]], failAfterFirst: true },
    { version: "8", pages: [[3, 1], [2]] },
  ])
  await f.controller.request("initial")
  const state = f.controller.getSnapshot()
  assert.deepEqual(state.ids, ["3", "1", "2"])
  assert.equal(state.pinOrderVersion, "8")
  assert.equal(state.error, null)
  assert.deepEqual(f.trace, ["page:7:0", "page:7:1", "page:8:0", "page:8:1"])
})

test("置顶区续页携带新的顺序版本时舍弃本轮候选，不与旧顺序混排", async () => {
  const f = pinnedFixture([
    { version: "7", pages: [[1, 2], [3]], laterVersion: "8" },
    { version: "8", pages: [[2, 3], [1]] },
  ])
  await f.controller.request("initial")
  assert.deepEqual(f.controller.getSnapshot().ids, ["2", "3", "1"])
  assert.equal(f.controller.getSnapshot().pinOrderVersion, "8")
  assert.deepEqual(f.trace, ["page:7:0", "page:7:1", "page:8:0", "page:8:1"])
})

test("置顶区持续读取失败时保留已确认顺序并记录错误", async () => {
  const f = pinnedFixture([
    { version: "7", pages: [[1, 2]] },
    { version: "8", pages: [[2, 1], [3]], failAfterFirst: true },
  ])
  await f.controller.request("initial")
  await f.controller.request("poll")
  const state = f.controller.getSnapshot()
  assert.deepEqual(state.ids, ["1", "2"])
  assert.equal(state.pinOrderVersion, "7")
  assert.equal(state.error, "poll")
})

test("置顶区重排以前驱变化识别被移动的行，保位锚点落在未移动的邻居", async () => {
  const f = pinnedFixture([
    { version: "7", pages: [[1, 2, 3]] },
    { version: "8", pages: [[3, 1, 2]] },
  ])
  await f.controller.request("initial")
  await f.controller.request("poll")
  assert.deepEqual(f.controller.getSnapshot().ids, ["3", "1", "2"])
  const moved = f.restored()![1]
  assert.ok(moved.has("3"))
  assert.ok(moved.has("1"))
  assert.ok(!moved.has("2"))
  assert.equal(f.restored()![2], false)
})

test("拖动期间到达的远端重排不提前发布顺序版本，展示顺序与写入版本保持同一份快照", async () => {
  const f = pinnedFixture([
    { version: "7", pages: [[1, 2, 3]] },
    { version: "8", pages: [[3, 1, 2]] },
  ])
  await f.controller.request("initial")
  f.interact(true)
  await f.controller.request("poll")
  assert.deepEqual(f.controller.getSnapshot().ids, ["1", "2", "3"])
  assert.equal(f.controller.getSnapshot().pinOrderVersion, "7")
  f.interact(false)
  f.controller.settle()
  assert.deepEqual(f.controller.getSnapshot().ids, ["3", "1", "2"])
  assert.equal(f.controller.getSnapshot().pinOrderVersion, "8")
})

test("本人置顶写入后的重读在操作中立即应用，连续拖动使用最新顺序版本", async () => {
  const f = pinnedFixture([
    { version: "7", pages: [[1, 2, 3]] },
    { version: "8", pages: [[2, 1, 3]] },
    { version: "9", pages: [[2, 3, 1]] },
  ])
  await f.controller.request("initial")
  // 手指已按上下一个手柄时，本人写入后的重读仍立即更新顺序与版本。
  f.interact(true)
  await f.controller.refreshOwnWrite()
  assert.deepEqual(f.controller.getSnapshot().ids, ["2", "1", "3"])
  assert.equal(f.controller.getSnapshot().pinOrderVersion, "8")
  // 本人写入的重读结束后，远端变化仍在操作中暂缓。
  await f.controller.request("poll")
  assert.deepEqual(f.controller.getSnapshot().ids, ["2", "1", "3"])
  assert.equal(f.controller.getSnapshot().pinOrderVersion, "8")
})

test("写入前已在途的重读不代替本人写入后的重读，只有写入后的结果立即应用", async () => {
  const f = pinnedFixture([
    { version: "7", pages: [[1, 2, 3]] },
    { version: "8", pages: [[2, 1, 3]] },
    { version: "9", pages: [[2, 3, 1]] },
  ])
  await f.controller.request("initial")
  f.interact(true)
  // 版本 8 的重读在写入前发出，写入完成后再请求本人写入的重读。
  const earlier = f.controller.request("refresh")
  const own = f.controller.refreshOwnWrite()
  await Promise.all([earlier, own])
  assert.deepEqual(f.trace, ["page:7:0", "page:8:0", "page:9:0"])
  assert.deepEqual(f.controller.getSnapshot().ids, ["2", "3", "1"])
  assert.equal(f.controller.getSnapshot().pinOrderVersion, "9")
})
