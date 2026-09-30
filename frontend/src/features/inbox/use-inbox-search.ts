/** 收件箱检索结果与会话名称搜索分页查询，以及全局搜索模态的检索状态与键盘选择。 */
import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react"

import {
  InboxPartition,
  InboxScope,
  InboxSearchRange,
  readInboxConversations,
  searchInbox,
  type InboxConversationData,
  type InboxQuery,
  type InboxSearchResultData,
} from "@/api"
import {
  normalizeInboxQuery,
  readableInboxQuery,
  type InboxQueryInput,
} from "@/features/inbox/inbox-query"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDebouncedValue } from "@/hooks/use-debounced-value"
import { useResource } from "@/hooks/use-resource"

const searchInputDelay = 200

export type InboxSearchType = "all" | "conversations" | "messages" | "people"
export type InboxSearchMessageData = InboxSearchResultData["messages"][number]
export type InboxSearchPersonData = InboxSearchResultData["people"][number]

/** 搜索模式中可用键盘选择和打开的结果项。 */
export type InboxSearchItem =
  | { kind: "conversation"; conversation: InboxConversationData }
  | { kind: "message"; message: InboxSearchMessageData }
  | { kind: "person"; person: InboxSearchPersonData }

export type InboxSearchState = ReturnType<typeof useInboxSearch>

/** 按检索文本、范围和类型读取分组结果与最近打开会话，只返回与当前输入一致的结果；会话类型给出名称搜索分页查询。 */
export function useInboxSearchResults({
  active,
  text,
  range: selectedRange,
  conversationId,
  type,
  query,
  recentConversationIds,
}: {
  active: boolean
  text: string
  range: InboxSearchRange
  conversationId: string
  type: InboxSearchType
  query: InboxQueryInput
  recentConversationIds: string[]
}) {
  const trimmedText = text.trim()
  // 输入停顿后再检索；清空输入立即结束上一次检索。
  const searchedText = useDebouncedValue(trimmedText, searchInputDelay, trimmedText === "")
  // 未指定列表范围时列表检索与全部消息等价，只保留全部消息。
  const listRange = query.scope !== InboxScope.$zero
  const range =
    (selectedRange === InboxSearchRange.InboxSearchRangeList && !listRange) ||
    (selectedRange === InboxSearchRange.InboxSearchRangeConversation && !conversationId)
      ? InboxSearchRange.InboxSearchRangeReadable
      : selectedRange
  const conversationRange = range === InboxSearchRange.InboxSearchRangeConversation
  // 会话类型下按名称搜索分页读取全部命中会话，不读取分组检索结果。
  const paged = !conversationRange && type === "conversations"

  const searchParameters = {
    query: searchedText,
    range,
    conversationId: conversationRange ? conversationId : "",
    ...(range === InboxSearchRange.InboxSearchRangeList ? query : {}),
  }
  const results = useResource(
    resourceKeys.inboxSearch(searchParameters),
    (signal) => searchInbox(searchParameters, signal),
    { enabled: active && searchedText !== "" && !paged, staleTime: 0 },
  )
  // 名称搜索沿用检索范围：列表范围带当前筛选，可读范围不带其他列表筛选。
  const nameQuery = normalizeInboxQuery({
    ...(range === InboxSearchRange.InboxSearchRangeList ? query : readableInboxQuery),
    partition: InboxPartition.InboxPartitionAll,
    search: searchedText,
    searchRange: range,
  })
  const showRecent = active && trimmedText === "" && !conversationRange
  // 最近会话只核对当前筛选下的列表资格，不受置顶分区限制。
  const recentQuery: InboxQuery = normalizeInboxQuery({ ...query, partition: InboxPartition.InboxPartitionAll })
  const recent = useResource(
    resourceKeys.recentConversations({ query: recentQuery, conversationIds: recentConversationIds }),
    (signal) => readInboxConversations({ query: recentQuery, conversationIds: recentConversationIds }, signal),
    { enabled: showRecent && recentConversationIds.length > 0, staleTime: 0 },
  )

  // 只展示与当前输入一致的检索结果。
  const current = active && trimmedText !== "" && trimmedText === searchedText
  const data = current && !paged ? results.data : undefined
  return {
    listRange,
    range,
    searchedText,
    showRecent,
    paged: active && paged && trimmedText !== "",
    nameQuery,
    recentConversations: showRecent
      ? (recent.data?.results ?? []).flatMap((result) => (result.conversation ? [result.conversation] : []))
      : [],
    conversations: data && !conversationRange && (type === "all" || type === "conversations") ? data.conversations : [],
    messages: data && (conversationRange || type === "all" || type === "messages") ? data.messages : [],
    people: data && !conversationRange && (type === "all" || type === "people") ? data.people : [],
    pending: trimmedText !== "" && (!current || (!paged && results.loading)),
    error: current && !paged ? results.error : null,
    retry: results.refresh,
  }
}

/** 管理全局搜索的检索状态、按范围读取结果和最近打开会话，并提供跨分组的键盘选择。 */
export function useInboxSearch({
  conversationId,
  recentConversationIds,
  onOpen,
}: {
  conversationId: string
  recentConversationIds: string[]
  onOpen: (item: InboxSearchItem) => void
}) {
  const [text, setText] = useState("")
  const [type, setType] = useState<InboxSearchType>("all")
  const [activeIndex, setActiveIndex] = useState(0)
  const [pagedConversations, setPagedConversations] = useState<InboxConversationData[]>([])
  const typeIndexes = useRef(new Map<InboxSearchType, number>())
  const results = useInboxSearchResults({
    active: true,
    text,
    // 从会话进入时只检索该会话，其余情况检索全部可读消息。
    range: conversationId
      ? InboxSearchRange.InboxSearchRangeConversation
      : InboxSearchRange.InboxSearchRangeReadable,
    conversationId,
    type,
    query: readableInboxQuery,
    recentConversationIds,
  })
  const { range, searchedText, showRecent } = results
  const items: InboxSearchItem[] = [
    ...(showRecent ? results.recentConversations : results.paged ? pagedConversations : results.conversations).map((conversation) => ({
      kind: "conversation" as const,
      conversation,
    })),
    ...results.messages.map((message) => ({ kind: "message" as const, message })),
    ...results.people.map((person) => ({ kind: "person" as const, person })),
  ]
  const selectedIndex = Math.min(activeIndex, items.length - 1)

  useEffect(() => {
    typeIndexes.current.clear()
    setActiveIndex(0)
  }, [searchedText, range, showRecent])

  /** 修改检索词；处于会话分页时回到分组结果。 */
  const changeText = useCallback(
    (value: string) => {
      setText(value)
      if (type === "conversations") {
        typeIndexes.current.clear()
        setType("all")
      }
    },
    [type],
  )

  /** 切换结果类型，回到查看过的类型时恢复其原选中项。 */
  const selectType = useCallback(
    (next: InboxSearchType) => {
      typeIndexes.current.set(type, selectedIndex)
      setType(next)
      setActiveIndex(typeIndexes.current.get(next) ?? 0)
    },
    [type, selectedIndex],
  )

  /** 在搜索框内用方向键跨分组选择，Enter 打开。 */
  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.nativeEvent.isComposing) return
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault()
      if (items.length === 0) return
      const step = event.key === "ArrowDown" ? 1 : -1
      setActiveIndex((selectedIndex + step + items.length) % items.length)
    } else if (event.key === "Enter" && items[selectedIndex]) {
      event.preventDefault()
      onOpen(items[selectedIndex])
    }
  }

  return {
    text,
    setText: changeText,
    setType: selectType,
    showRecent,
    paged: results.paged,
    nameQuery: results.nameQuery,
    setPagedConversations,
    recentConversations: results.recentConversations,
    conversations: results.conversations,
    messages: results.messages,
    people: results.people,
    items,
    selectedIndex,
    setActiveIndex,
    pending: results.pending,
    error: results.error,
    retry: results.retry,
    open: onOpen,
    handleKeyDown,
  }
}
