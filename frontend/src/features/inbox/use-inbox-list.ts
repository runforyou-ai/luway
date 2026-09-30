/** 将列表控制器接入统一 Query 缓存、变更失效和会话资源清理。 */
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useSyncExternalStore } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { InboxPartition, getInboxContext, loadInbox, readInboxConversations, readInboxWindow, type InboxQuery, type Identity, type InboxConversationResults } from "@/api"
import { useRealtimeSyncActive } from "@/contexts/realtime-sync-context"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceReader } from "@/hooks/use-resource"
import { clearConversationResources } from "./conversation-resources"
import { InboxListController, type InboxListBookmark, type InboxListOperation } from "./inbox-list-controller"
import { combineInboxPartitions, regularPartitionRestore } from "./inbox-partitions"
import { memberChatPollingInterval } from "./use-member-chat-polling"

import type { useInboxListViewport } from "./use-inbox-list-viewport"

export type InboxListViewport = ReturnType<typeof useInboxListViewport>
type InboxListOptions = {
  identity: Identity
  active: boolean
  unavailable?: (id: string) => void
  history?: Map<string, InboxListBookmark>
  selectedConversationId?: string
}

/** 每个分区查询持有独立控制器与浏览状态，业务摘要仅从当前批量 Query 读取；region 表示按置顶顺序整区读取。 */
function useInboxPartition(input: InboxQuery, viewport: InboxListViewport, options: InboxListOptions, region = false) {
  const { identity, active, history } = options
  const client = useQueryClient()
  const realtime = useRealtimeSyncActive()
  const query = useMemo(() => input, [input.partition, input.scope, input.pendingKind, input.queueFilter, input.queueTeamId, input.channelId, input.source, input.audience, input.serviceStatus, input.assigneeFilter, input.assigneeIdentityId, input.kinds?.join(","), input.search, input.searchRange])
  const owner = useMemo(() => ({ organizationId: identity.organization.id, userId: identity.user.id }), [identity.organization.id, identity.user.id])
  const view = useId()
  const headKey = resourceKeys.inbox({ ...owner, ...query })
  // 为首页查询登记观察者，让会话资源清理按已挂载列表处理该 key。
  useResource(headKey, () => loadInbox(query), { enabled: false })
  const read = useResourceReader()
  const historyKey = JSON.stringify({ ...owner, ...query })
  const callbacks = useRef({ viewport, options })
  callbacks.current = { viewport, options }
  const controller = useMemo(() => {
    const bookmark = history?.get(historyKey)
    const cached = bookmark && client.getQueryData(resourceKeys.inboxConversations({ ...owner, query, conversationIds: bookmark.state.rowIds })) !== undefined
    // 选中会话只在控制器创建时读取，作为进入列表时的定位目标。
    const locateId = callbacks.current.options.selectedConversationId || null
    return new InboxListController({
    page: (cursor = "", beforeCursor = "") => read(
      resourceKeys.inbox({ ...owner, ...query, ...(cursor || beforeCursor ? { cursor, beforeCursor } : {}) }),
      () => loadInbox({ ...query, cursor, beforeCursor }),
    ),
    window: (startCursor, endCursor) => read(
      resourceKeys.inboxWindow({ ...owner, query, startCursor, endCursor }),
      () => readInboxWindow({ query, startCursor, endCursor }),
    ),
    context: async (anchor) => {
      const parameters = { query, anchorId: anchor.id, anchorCursor: anchor.cursor, beforeLimit: 25, afterLimit: 25 }
      return (await read(resourceKeys.inboxContext({ ...owner, ...parameters }), () => getInboxContext(parameters))).window
    },
    rows: (conversationIds) => read(
      resourceKeys.inboxConversations({ ...owner, query, conversationIds }),
      (signal) => readInboxConversations({ query, conversationIds }, signal),
    ),
    capture: () => callbacks.current.viewport.capture(),
    atTop: () => callbacks.current.viewport.atTop(),
    interacting: () => callbacks.current.viewport.interacting(),
    restore: (...args) => callbacks.current.viewport.restore(...args),
    unavailable: (ids) => {
      for (const id of ids) {
        callbacks.current.options.unavailable?.(id)
        clearConversationResources(client, id)
        void client.resetQueries({ queryKey: resourceKeys.conversationSummary(id) })
      }
    },
  }, query, { bookmark, cached, locateId, region })
  }, [client, owner, query, read, history, historyKey, region])
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot)
  const rows = useResource(
    resourceKeys.inboxConversations({ ...owner, query, conversationIds: state.rowIds }),
    (signal) => readInboxConversations({ query, conversationIds: state.rowIds }, signal),
    { enabled: false },
  )

  useLayoutEffect(() => () => {
    if (history) history.set(historyKey, controller.remember())
  }, [controller, history, historyKey])
  useEffect(() => () => controller.dispose(), [controller])
  useEffect(() => client.getQueryCache().subscribe((event) => {
    const key = event.query.queryKey
    if (event.type === "updated" && key[0] === resourceKeys.conversationSummary()[0] && event.query.state.data === null) {
      controller.removeUnavailable(String(key[1]))
    }
    // 新批次接管展示后移除含失权摘要的旧缓存。
    if (event.type === "observerRemoved" && key[0] === resourceKeys.inboxConversations()[0] && event.query.getObserversCount() === 0) {
      const results = (event.query.state.data as InboxConversationResults | undefined)?.results
      if (results?.some((row) => row.conversation && controller.getSnapshot().unavailableIds.includes(row.id))) client.removeQueries({ queryKey: key, exact: true })
    }
  }), [client, controller])
  // 列表窗口重读登记为挂载中的查询，首次读取、同步失效、失败重试与前台轮询共用同一入口。
  const sync = useResource(
    resourceKeys.inbox({ ...owner, ...query, view }),
    async () => {
      await controller.request("poll")
      const { error, revision } = controller.getSnapshot()
      // 控制器读取失败时该查询同样失败，由统一的失败重试接管。
      if (error) throw new Error(`读取收件箱窗口失败：${error}`)
      return revision
    },
    {
      staleTime: 0,
      refetchInterval: active && !realtime ? memberChatPollingInterval : false,
      refetchOnWindowFocus: false,
    },
  )
  const refresh = sync.refresh
  const wasActive = useRef(active)
  useEffect(() => {
    // 未接入实时同步的外壳回到前台时立即重读当前窗口。
    if (active && !wasActive.current && !realtime) void refresh({ cancelRefetch: false })
    wasActive.current = active
  }, [active, realtime, refresh])

  const conversations = new Map(rows.data?.results.flatMap((row) => row.availability === "matching" && row.conversation ? [[row.id, row.conversation] as const] : []) ?? [])
  return { state, controller, conversations: state.ids.flatMap((id) => conversations.has(id) ? [conversations.get(id)!] : []) }
}

type InboxPartitionView = ReturnType<typeof useInboxPartition>

/** 把已展示行的位置与滚动、空闲事件接到视口；分页由 paging 分区负责，回到顶部时全部分区重读。 */
function bindViewport(viewport: InboxListViewport, positions: InboxPartitionView["state"]["positions"], partitions: InboxPartitionView[], paging: InboxPartitionView) {
  viewport.positions.current = positions
  viewport.events.current = {
    idle: () => partitions.forEach((partition) => partition.controller.settle()),
    scroll: (container, enteredTop) => {
      const current = paging.controller.getSnapshot()
      if (current.error || !container.clientHeight) return
      if (container.scrollTop <= 120 && current.hasBefore) void paging.controller.request("before")
      else if (enteredTop) partitions.forEach((partition) => void partition.controller.request("refresh"))
      else if (container.scrollHeight - container.scrollTop - container.clientHeight <= 120 && current.hasAfter) void paging.controller.request("after")
    },
  }
}

/** 按查询自身的排序读取一条列表，不区分置顶；settlePin 在置顶写入后重读列表。 */
export function useInboxList(input: InboxQuery, viewport: InboxListViewport, options: InboxListOptions) {
  const list = useInboxPartition(input, viewport, options)
  bindViewport(viewport, list.state.positions, [list], list)
  const controller = list.controller
  const settlePin = useCallback((_pinned: boolean) => controller.refreshOwnWrite(), [controller])
  return {
    ...list.state,
    conversations: list.conversations,
    pinnedIds: [] as string[],
    request: list.controller.request,
    retry: list.controller.retry,
    settlePin,
  }
}

export type InboxList = ReturnType<typeof useInboxList>

/** 置顶区与普通区各自读取，合并为先置顶后普通的一条列表；settlePin 在置顶写入后先重读目标分区，会话不在两区之间短暂消失，本人写入后的重读立即提交顺序与版本。 */
export function usePartitionedInboxList(input: InboxQuery, viewport: InboxListViewport, options: InboxListOptions) {
  const located = useRef({ pinnedIds: [] as string[], locateId: "" })
  const regularViewport = useMemo(() => ({
    ...viewport,
    restore: regularPartitionRestore(viewport.restore, () => located.current.pinnedIds, () => located.current.locateId),
  }), [viewport])
  const pinned = useInboxPartition({ ...input, partition: InboxPartition.InboxPartitionPinned }, viewport, options, true)
  const regular = useInboxPartition({ ...input, partition: InboxPartition.InboxPartitionRegular }, regularViewport, options)
  located.current = { pinnedIds: pinned.state.ids, locateId: options.selectedConversationId ?? "" }
  const combined = combineInboxPartitions(pinned, regular)
  bindViewport(viewport, combined.positions, [pinned, regular], regular)
  const pinnedController = pinned.controller
  const regularController = regular.controller
  const request = useCallback((operation: InboxListOperation) => operation === "before" || operation === "after"
    ? regularController.request(operation)
    : Promise.all([pinnedController.request(operation), regularController.request(operation)]).then(() => undefined), [pinnedController, regularController])
  const retry = useCallback(() => {
    if (pinnedController.getSnapshot().error) void pinnedController.retry()
    return regularController.retry()
  }, [pinnedController, regularController])
  const settlePin = useCallback(async (nowPinned: boolean) => {
    const [first, second] = nowPinned ? [pinnedController, regularController] : [regularController, pinnedController]
    await first.refreshOwnWrite()
    await second.refreshOwnWrite()
  }, [pinnedController, regularController])
  return { ...combined, request, retry, settlePin }
}

export type PartitionedInboxList = ReturnType<typeof usePartitionedInboxList>
