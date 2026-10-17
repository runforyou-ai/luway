/** 当前设备记录的最近搜索词，供搜索空态与输入提示使用。 */
import { useCallback, useEffect, useRef, useState } from "react"

import { readLocalPreference, writeLocalPreference } from "@/lib/local-preference"

const searchQueryLimit = 10

export type StoredSearchQuery = {
  text: string
  conversationId: string
  lastUsedAt: number
}

/** 读取已保存的搜索词列表，忽略无法识别的项。 */
function readStoredQueries(storageKey: string): StoredSearchQuery[] {
  const stored = readLocalPreference(storageKey)
  if (!Array.isArray(stored)) return []
  return stored
    .flatMap((item): StoredSearchQuery[] => {
      if (!item || typeof item !== "object") return []
      const text = "text" in item && typeof item.text === "string" ? item.text.trim() : ""
      if (!text) return []
      const conversationId =
        "conversationId" in item && typeof item.conversationId === "string" ? item.conversationId : ""
      const lastUsedAt =
        "lastUsedAt" in item && typeof item.lastUsedAt === "number" ? item.lastUsedAt : 0
      return [{ text, conversationId, lastUsedAt }]
    })
    .slice(0, searchQueryLimit)
}

/** 按当前会话范围与输入过滤排序最近搜索词；同会话最近使用的排在前面。 */
export function rankSearchQueries(
  queries: StoredSearchQuery[],
  conversationId: string,
  filterText = "",
): StoredSearchQuery[] {
  const needle = filterText.trim().toLowerCase()
  const matched = needle
    ? queries.filter((query) => query.text.toLowerCase().includes(needle))
    : queries
  return [...matched].sort((left, right) => {
    if (conversationId) {
      const leftMatch = left.conversationId === conversationId ? 1 : 0
      const rightMatch = right.conversationId === conversationId ? 1 : 0
      if (leftMatch !== rightMatch) return rightMatch - leftMatch
    }
    return right.lastUsedAt - left.lastUsedAt
  })
}

/** 读取并更新当前身份在本机最近搜索过的关键词。 */
export function useSearchQueries(identityId: string) {
  const storageKey = `app.inbox.search-queries.${identityId}`
  const [queries, setQueries] = useState<StoredSearchQuery[]>(() => readStoredQueries(storageKey))
  const queriesRef = useRef(queries)
  const storageKeyRef = useRef(storageKey)
  queriesRef.current = queries

  useEffect(() => {
    if (storageKeyRef.current === storageKey) return
    storageKeyRef.current = storageKey
    const next = readStoredQueries(storageKey)
    queriesRef.current = next
    setQueries(next)
  }, [storageKey])

  const record = useCallback(
    (text: string, conversationId = "") => {
      const trimmed = text.trim()
      if (!trimmed) return
      const next: StoredSearchQuery[] = [
        { text: trimmed, conversationId, lastUsedAt: Date.now() },
        ...queriesRef.current.filter((query) => query.text.toLowerCase() !== trimmed.toLowerCase()),
      ].slice(0, searchQueryLimit)
      writeLocalPreference(storageKey, next)
      queriesRef.current = next
      setQueries(next)
    },
    [storageKey],
  )

  const remove = useCallback(
    (text: string) => {
      const normalized = text.trim().toLowerCase()
      if (!normalized) return
      const next = queriesRef.current.filter((query) => query.text.toLowerCase() !== normalized)
      writeLocalPreference(storageKey, next)
      queriesRef.current = next
      setQueries(next)
    },
    [storageKey],
  )

  const clear = useCallback(() => {
    writeLocalPreference(storageKey, [])
    queriesRef.current = []
    setQueries([])
  }, [storageKey])

  return { queries, record, remove, clear }
}
