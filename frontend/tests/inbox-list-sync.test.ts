/** 用真实 QueryClient 验证收件箱列表重读经缓存层的连续失效不丢更新。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { QueryClient, QueryObserver } from "@tanstack/react-query"
import { ResourceRefresher } from "../src/features/session/resource-refresher.ts"
import { InboxListController, type InboxListPorts } from "../src/features/inbox/inbox-list-controller.ts"
import { resourceKeys } from "../src/hooks/resource-keys.ts"
import type { InboxConversationData, InboxQuery } from "../src/api/index.ts"

/** 等待已排队的微任务与 I/O 回调执行完毕。 */
function flush() {
  return new Promise((resolve) => setImmediate(resolve))
}

/** 按 use-inbox-list 的接线装配控制器、同步查询与刷新器。 */
function setup() {
  const calls: string[] = []
  const gates: PromiseWithResolvers<void>[] = []
  let order = ["1", "2", "3"]
  let gating = false
  const conversation = (id: string) =>
    ({ id, positionCursor: `p${id}`, lastActivityAt: `2026-09-09T00:00:0${id}Z` }) as InboxConversationData
  // 读取结果取发起时刻的顺序，读取期间发生的变化只能由随后的重读取得。
  const snapshot = () => ({
    conversations: order.map(conversation),
    startCursor: `p${order[0]}`,
    endCursor: `p${order[order.length - 1]}`,
    hasBefore: false,
    hasAfter: false,
  })
  const hold = async () => {
    if (!gating) return
    const gate = Promise.withResolvers<void>()
    gates.push(gate)
    await gate.promise
  }
  const ports: InboxListPorts = {
    page: async () => {
      calls.push("page")
      const window = snapshot()
      await hold()
      return { ...window, hasMore: false, nextCursor: window.endCursor, unreadCount: 0, attentionUnreadCount: 0 }
    },
    window: async () => {
      calls.push("window")
      const window = snapshot()
      await hold()
      return window
    },
    context: async () => snapshot(),
    rows: async (ids) =>
      ({ results: ids.map((id) => ({ id, availability: "matching", conversation: conversation(id) })) }) as Awaited<
        ReturnType<InboxListPorts["rows"]>
      >,
    capture: () => null,
    atTop: () => true,
    interacting: () => false,
    restore: () => {},
    unavailable: () => {},
  }
  const controller = new InboxListController(ports, { scope: "chat" } as InboxQuery)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  const syncKey = resourceKeys.inbox({ organizationId: "o1", userId: "u1", view: "list" })
  const observer = new QueryObserver(client, {
    queryKey: syncKey,
    queryFn: async () => {
      await controller.request("poll")
      const { error, revision } = controller.getSnapshot()
      if (error) throw new Error(`读取收件箱窗口失败：${error}`)
      return revision
    },
    staleTime: 0,
  })
  const unsubscribe = observer.subscribe(() => {})
  return {
    controller,
    calls,
    unsubscribe,
    refresher: new ResourceRefresher(client),
    surface: (id: string) => { order = [id, ...order] },
    gate: (value: boolean) => { gating = value },
    release: () => { gates.forEach((gate) => gate.resolve()); gates.length = 0 },
  }
}

test("读取在途时到达的失效不被缓存层吞掉，随后的重读取得上浮会话", async () => {
  const f = setup()
  try {
    await flush()
    assert.deepEqual(f.controller.getSnapshot().ids, ["1", "2", "3"])
    const loaded = f.calls.length

    f.gate(true)
    f.refresher.invalidate(resourceKeys.inbox())
    await flush()
    // 第二条通知在首次重读完成前到达，该查询此时已处于失效状态。
    f.surface("9")
    f.refresher.invalidate(resourceKeys.inbox())
    f.gate(false)
    f.release()
    await flush()
    await flush()

    assert.deepEqual(f.controller.getSnapshot().ids, ["9", "1", "2", "3"])
    assert.equal(f.calls.slice(loaded).filter((call) => call === "page").length, 2)
  } finally {
    f.unsubscribe()
  }
})
