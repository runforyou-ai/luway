/** 把置顶区与普通区的窗口状态合并为一条列表，协调两个分区对同一视口的首次定位，并生成相对可见邻居的置顶位置命令。 */
import type { InboxConversationData } from "@/api"
import type { InboxListPorts, InboxListState } from "./inbox-list-controller"

export type InboxPartitionSnapshot = { state: InboxListState; conversations: InboxConversationData[] }

/** 先置顶后普通地合并两个分区：普通区停在深处窗口时只展示普通区，置顶区首次读取结束后才接上普通区，同一会话只出现一次。 */
export function combineInboxPartitions(pinned: InboxPartitionSnapshot, regular: InboxPartitionSnapshot) {
  const pinnedReady = pinned.state.revision > 0 || pinned.state.error !== null
  const pinnedRows = regular.state.hasBefore ? [] : pinned.conversations
  const pinnedIds = pinnedRows.map((row) => row.id)
  const regularRows = pinnedReady ? regular.conversations.filter((row) => !pinnedIds.includes(row.id)) : []
  const regularIds = regularRows.map((row) => row.id)
  return {
    ...regular.state,
    ids: [...pinnedIds, ...regularIds],
    positions: [
      ...pinned.state.positions.filter((row) => pinnedIds.includes(row.id)),
      ...regular.state.positions.filter((row) => regularIds.includes(row.id)),
    ],
    pinnedIds,
    conversations: [...pinnedRows, ...regularRows],
    pinOrderVersion: pinned.state.pinOrderVersion,
    // 置顶区就绪前不触发普通区的自动补页。
    hasBefore: pinnedReady && regular.state.hasBefore,
    hasAfter: pinnedReady && regular.state.hasAfter,
    operation: regular.state.operation ?? pinned.state.operation,
    error: regular.state.error ?? pinned.state.error,
    revision: pinnedReady && regular.state.revision > 0 ? pinned.state.revision + regular.state.revision : 0,
  }
}

/** 普通区的视口恢复入口：定位目标属于置顶区时，普通区读不到锚点而回到首页的置顶意图不生效，定位由置顶区完成。 */
export function regularPartitionRestore(restore: InboxListPorts["restore"], pinnedIds: () => string[], locateId: () => string): InboxListPorts["restore"] {
  return (anchor, moved, top) => restore(anchor, moved, top && !(anchor === null && pinnedIds().includes(locateId())))
}

/** 把置顶会话在可见置顶顺序中移到 to 位置时的落点：放到 neighborId 之前或之后；位置无效或不变时返回 null。 */
export function pinMoveTarget(order: string[], conversationId: string, to: number) {
  const from = order.indexOf(conversationId)
  if (from < 0 || to < 0 || to >= order.length || from === to) return null
  return { before: from > to, neighborId: order[to] }
}
