/** 收件箱路由：待处理与全部两个页签各自读取列表并记住筛选。 */
import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"

import { InboxScope, type InboxQuery } from "@/api"
import { useWorkspace } from "@/contexts/workspace-context"
import type { ConversationLocateTarget } from "./conversation-timeline"
import { useAttachmentQueue } from "@/contexts/attachment-queue-context"
import { useOutgoingMessageStore } from "@/contexts/outgoing-message-context"
import { useMemberChatPollingActive } from "./use-member-chat-polling"
import { InboxPage, type InboxQueryChange } from "@/features/inbox/inbox-page"
import {
  inboxQueryFromSearch,
  normalizeInboxQuery,
  writeInboxQuerySearch,
  type InboxTab,
  type NormalizedInboxQuery,
} from "./inbox-query"
import { useInboxList, usePartitionedInboxList } from "./use-inbox-list"
import { useInboxListViewport } from "./use-inbox-list-viewport"

/** 各用户各页签上次使用的地址参数，切换页签时恢复，在本次页面会话内保留。 */
const tabSearches = new Map<string, string>()

type InboxTabProps = {
  query: NormalizedInboxQuery
  selectedConversationId: string
  locateMessage: ConversationLocateTarget | null
  onSelectedConversationChange: (conversationId: string, replace?: boolean) => void
  onTabChange: (tab: InboxTab) => void
  onQueryChange: (changes: InboxQueryChange) => void
}

/** 读取列表所需的会话资源清理与轮询选项。 */
function useInboxListOptions(selectedConversationId: string) {
  const { identity } = useWorkspace()
  const queue = useAttachmentQueue()
  const outgoingStore = useOutgoingMessageStore()
  const active = useMemberChatPollingActive()
  return {
    identity, active, selectedConversationId,
    unavailable: (id: string) => {
      queue?.forgetConversation(id)
      outgoingStore.forgetConversation(id)
    },
  }
}

/** 待处理页签按等待起点读取一条列表。 */
function PendingInbox(props: InboxTabProps) {
  const viewport = useInboxListViewport()
  const list = useInboxList(props.query, viewport, useInboxListOptions(props.selectedConversationId))
  return <InboxPage list={list} listViewport={viewport} {...props} />
}

/** 全部页签按置顶区在前、最近活动在后读取列表。 */
function AllInbox(props: InboxTabProps) {
  const viewport = useInboxListViewport()
  const list = usePartitionedInboxList(props.query, viewport, useInboxListOptions(props.selectedConversationId))
  return <InboxPage list={list} listViewport={viewport} {...props} />
}

/** 以规范化查询作为选择历史的保存键。 */
function browseKey(query: InboxQuery) {
  return JSON.stringify(query)
}

/** 加载并显示收件箱。 */
export function InboxRoute() {
  const { identity } = useWorkspace()
  const selections = useRef(new Map<string, string>())
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedConversationId = searchParams.get("conversation") ?? ""
  const locateMessageId = searchParams.get("message") ?? ""
  const locateNonce = useRef(0)
  const [locateMessage, setLocateMessage] = useState<ConversationLocateTarget | null>(null)
  const query = inboxQueryFromSearch(searchParams)

  useEffect(() => {
    // 搜索结果带来的消息定位一次性生效，随后从地址中移除。
    if (!locateMessageId) return
    locateNonce.current++
    setLocateMessage({ messageId: locateMessageId, nonce: locateNonce.current })
    setSearchParams((current) => {
      const next = new URLSearchParams(current)
      next.delete("message")
      return next
    }, { replace: true })
  }, [locateMessageId, setSearchParams])

  useEffect(() => {
    // 记住当前页签的筛选与选中会话。
    tabSearches.set(`${identity.user.id}:${query.scope}`, searchParams.toString())
  }, [identity.user.id, query.scope, searchParams])

  /** 更新当前页签的筛选与选中会话。 */
  function updateQuery(changes: InboxQueryChange) {
    const { conversationId, replace, ...filters } = changes
    const next = normalizeInboxQuery({ ...query, ...filters })
    const currentKey = browseKey(query)
    const nextKey = browseKey(next)
    // 切换筛选时保存当前选择，恢复目标筛选的上次选择，无记录则保持未选中。
    selections.current.set(currentKey, selectedConversationId)
    const nextSelection = conversationId ?? (nextKey === currentKey ? selectedConversationId : selections.current.get(nextKey) ?? "")
    setSearchParams((current) => {
      const params = new URLSearchParams(current)
      writeInboxQuerySearch(params, next)
      if (nextSelection) params.set("conversation", nextSelection)
      else params.delete("conversation")
      return params
    }, { replace: replace ?? true })
  }

  /** 切换页签并恢复该页签上次的筛选与选中会话。 */
  function changeTab(tab: InboxTab) {
    if (tab === query.scope) return
    const params = new URLSearchParams(tabSearches.get(`${identity.user.id}:${tab}`) ?? "")
    params.set("tab", tab)
    setSearchParams(params, { replace: true })
  }

  /** 将当前会话同步到地址，支持刷新和前进后退恢复。 */
  function selectConversation(conversationId: string, replace = false) {
    setSearchParams((current) => {
      const next = new URLSearchParams(current)
      if (conversationId) next.set("conversation", conversationId)
      else next.delete("conversation")
      return next
    }, { replace })
  }

  const props: InboxTabProps = {
    query,
    selectedConversationId,
    locateMessage,
    onSelectedConversationChange: selectConversation,
    onTabChange: changeTab,
    onQueryChange: updateQuery,
  }
  return query.scope === InboxScope.InboxScopePending ? <PendingInbox {...props} /> : <AllInbox {...props} />
}
