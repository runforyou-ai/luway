/** 各端独立读取会话摘要，并在确认失权后清理共享资源。 */
import { useEffect, useRef } from "react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { getInboxConversation, isNotFoundApiError, type InboxConversationData } from "@/api"
import { useRealtimeSyncActive } from "@/contexts/realtime-sync-context"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { useAttachmentQueue } from "@/contexts/attachment-queue-context"
import { useOutgoingMessageStore } from "@/contexts/outgoing-message-context"
import { clearConversationResources } from "./conversation-resources"
import {
  memberChatPollingInterval,
  useMemberChatPollingActive,
} from "./use-member-chat-polling"

/** 将服务端确认的不可用结果保存为查询事实，网络错误保留既有详情。 */
export async function readConversationSummary(conversationID: string, signal?: AbortSignal) {
  try {
    return await getInboxConversation(conversationID, signal)
  } catch (error) {
    if (isNotFoundApiError(error)) return null
    throw error
  }
}

/** 从已加载的会话列表缓存中找到该会话，作为摘要读取完成前的占位。 */
function listedConversation(client: QueryClient, conversationID: string) {
  for (const [, data] of client.getQueriesData<{ conversations?: InboxConversationData[] }>({ queryKey: resourceKeys.inbox() })) {
    const conversation = data?.conversations?.find((item) => item.id === conversationID)
    if (conversation) return conversation
  }
  return undefined
}

/** 会话摘要读取结果，由会话详情及其宿主共用。 */
export type ConversationSummaryResource = ReturnType<typeof useConversationSummary>

/** 当前会话按变更通知或前台轮询发现资料变化，未接入实时同步时恢复前台后立即重读。 */
export function useConversationSummary(conversationID: string, requireWindowFocus = true) {
  const client = useQueryClient()
  const queue = useAttachmentQueue()
  const outgoingStore = useOutgoingMessageStore()
  const active = useMemberChatPollingActive({ requireWindowFocus })
  const previousActive = useRef(active)
  const realtime = useRealtimeSyncActive()
  // 接入实时同步的外壳由会话通知与失权通知失效摘要，切换会话时沿用缓存；其余外壳每次挂载重读并在前台轮询；不可用时继续读取，重新获得阅读资格后恢复详情。
  const resource = useResource(
    resourceKeys.conversationSummary(conversationID),
    (signal) => readConversationSummary(conversationID, signal),
    {
      enabled: Boolean(conversationID),
      staleTime: realtime ? Infinity : 0,
      refetchInterval: active && !realtime ? memberChatPollingInterval : false,
      refetchOnWindowFocus: false,
      // 首次读取完成前用列表中的这一行渲染会话头与输入区。
      placeholder: () => listedConversation(client, conversationID),
    },
  )
  const { data, refresh } = resource
  useEffect(() => {
    if (active && !previousActive.current && conversationID && !realtime) void refresh()
    previousActive.current = active
  }, [active, conversationID, realtime, refresh])
  useEffect(() => {
    if (data !== null || !conversationID) return
    queue?.forgetConversation(conversationID)
    outgoingStore.forgetConversation(conversationID)
    clearConversationResources(client, conversationID)
  }, [client, conversationID, data, queue, outgoingStore])
  return resource
}
