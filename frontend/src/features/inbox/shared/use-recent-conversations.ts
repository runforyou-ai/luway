/** 当前设备记录的最近打开会话。 */
import { useCallback, useState } from "react"

import { readLocalPreference, writeLocalPreference } from "@/lib/local-preference"

const recentConversationLimit = 6

/** 读取并更新当前身份在本机最近打开的会话编号，最新打开的排在最前。 */
export function useRecentConversations(identityId: string) {
  const storageKey = `app.inbox.recent.${identityId}`
  const [ids, setIds] = useState<string[]>(() => {
    const stored = readLocalPreference(storageKey)
    return Array.isArray(stored)
      ? stored.filter((id): id is string => typeof id === "string").slice(0, recentConversationLimit)
      : []
  })

  const record = useCallback(
    (conversationId: string) => {
      setIds((current) => {
        const next = [conversationId, ...current.filter((id) => id !== conversationId)].slice(0, recentConversationLimit)
        writeLocalPreference(storageKey, next)
        return next
      })
    },
    [storageKey],
  )

  return { ids, record }
}
