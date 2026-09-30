/** 验证置顶区与普通区的合并规则、两个分区对同一视口首次定位的协调，以及置顶移动落点。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { combineInboxPartitions, pinMoveTarget, regularPartitionRestore, type InboxPartitionSnapshot } from "../src/features/inbox/inbox-partitions.ts"
import type { InboxListPorts, InboxListState } from "../src/features/inbox/inbox-list-controller.ts"
import type { InboxConversationData } from "../src/api/index.ts"

/** 构造一个分区的窗口状态与已读取的会话摘要。 */
function partition(ids: string[], change: Partial<InboxListState> = {}): InboxPartitionSnapshot {
  return {
    state: {
      ids, positions: ids.map((id) => ({ id, positionCursor: `p${id}`, lastActivityAt: "" })), rowIds: ids, unavailableIds: [],
      startCursor: "", endCursor: "", hasBefore: false, hasAfter: false, attentionUnreadCount: 0, pendingCount: 0, pinOrderVersion: "",
      status: "ready", operation: null, error: null, revision: 1, ...change,
    },
    conversations: ids.map((id) => ({ id }) as InboxConversationData),
  }
}

test("置顶区在前、普通区在后，两区同时含有的会话只在置顶区出现一次", () => {
  const list = combineInboxPartitions(partition(["a", "b"], { pinOrderVersion: "9" }), partition(["b", "c", "d"], { hasAfter: true }))
  assert.deepEqual(list.ids, ["a", "b", "c", "d"])
  assert.deepEqual(list.conversations.map((row) => row.id), ["a", "b", "c", "d"])
  assert.deepEqual(list.positions.map((row) => row.id), ["a", "b", "c", "d"])
  assert.deepEqual(list.pinnedIds, ["a", "b"])
  assert.equal(list.pinOrderVersion, "9")
  assert.equal(list.hasAfter, true)
  assert.equal(list.revision, 2)
})

test("普通区停在深处窗口时只展示普通区，置顶行不接在深处窗口之上", () => {
  const list = combineInboxPartitions(partition(["a", "b"]), partition(["x", "y"], { hasBefore: true }))
  assert.deepEqual(list.ids, ["x", "y"])
  assert.deepEqual(list.pinnedIds, [])
  assert.equal(list.hasBefore, true)
})

test("置顶区首次读取结束前不接上普通区，也不触发补页与空态", () => {
  const list = combineInboxPartitions(partition([], { revision: 0 }), partition(["x", "y"], { hasAfter: true }))
  assert.deepEqual(list.ids, [])
  assert.equal(list.hasAfter, false)
  assert.equal(list.revision, 0)
})

test("置顶区首次读取失败时照常展示普通区并带出错误，普通区未就绪时整体仍按首次读取处理", () => {
  const failed = combineInboxPartitions(partition([], { revision: 0, error: "poll" }), partition(["x"]))
  assert.deepEqual(failed.ids, ["x"])
  assert.equal(failed.error, "poll")
  assert.equal(failed.revision, 1)
  const waiting = combineInboxPartitions(partition(["a"]), partition([], { revision: 0 }))
  assert.deepEqual(waiting.ids, ["a"])
  assert.equal(waiting.revision, 0)
})

test("定位目标属于置顶区时，普通区读不到锚点的回顶意图不覆盖置顶区的定位", () => {
  const calls: Parameters<InboxListPorts["restore"]>[] = []
  let pinnedIds: string[] = []
  const restore = regularPartitionRestore((...args) => { calls.push(args) }, () => pinnedIds, () => "n")
  // 置顶区尚未读到时普通区照常回到顶部，随后由置顶区的定位接管。
  restore(null, new Set(), true)
  pinnedIds = ["a", "n"]
  restore(null, new Set(), true)
  assert.deepEqual(calls.map((call) => call[2]), [true, false])
})

test("定位目标不在置顶区或普通区带有锚点时，普通区的恢复意图原样传递", () => {
  const calls: Parameters<InboxListPorts["restore"]>[] = []
  const anchor = { id: "x", cursor: "px", width: 1, height: 1, neighbors: [] }
  const restore = regularPartitionRestore((...args) => { calls.push(args) }, () => ["a"], () => "x")
  restore(null, new Set(), true)
  restore(anchor, new Set(["x"]), true)
  assert.deepEqual(calls.map((call) => call[2]), [true, true])
  assert.equal(calls[1][0], anchor)
})

test("上移与下移落在相对可见邻居的前后，隐藏项不参与", () => {
  // 全局顺序 A,X,B 中 X 被筛掉，可见顺序为 A,B。
  const order = ["A", "B"]
  assert.deepEqual(pinMoveTarget(order, "B", 0), { before: true, neighborId: "A" })
  assert.deepEqual(pinMoveTarget(order, "A", 1), { before: false, neighborId: "B" })
})

test("首项上移、末项下移、原位与不在置顶区的会话没有落点", () => {
  const order = ["A", "B", "C"]
  assert.equal(pinMoveTarget(order, "A", -1), null)
  assert.equal(pinMoveTarget(order, "C", 3), null)
  assert.equal(pinMoveTarget(order, "B", 1), null)
  assert.equal(pinMoveTarget(order, "Z", 0), null)
})
