/** 各端独立读取会话摘要，并在确认失权后清理共享资源。 */
import { useEffect } from "react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { getInboxConversation, isNotFoundApiError, type InboxConversation } from "@/api"
import { useAttachmentQueue } from "@/features/inbox/state/attachment-queue-context"
import { useOutgoingMessageStore } from "@/features/inbox/state/outgoing-message-context"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { clearConversationResources } from "./conversation-resources"

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
  for (const [, data] of client.getQueriesData<{ conversations?: InboxConversation[] }>({ queryKey: resourceKeys.inbox() })) {
    const conversation = data?.conversations?.find((item) => item.id === conversationID)
    if (conversation) return conversation
  }
  return undefined
}

/** 会话摘要读取结果，由会话详情及其宿主共用。 */
export type ConversationSummaryResource = ReturnType<typeof useConversationSummary>

/** 读取当前会话摘要，由会话通知与失权通知失效。 */
export function useConversationSummary(conversationID: string) {
  const client = useQueryClient()
  const queue = useAttachmentQueue()
  const outgoingStore = useOutgoingMessageStore()
  // 摘要由会话通知与失权通知失效，切换会话时沿用缓存；不可用时继续读取，重新获得阅读资格后恢复详情。
  const resource = useResource(
    resourceKeys.conversationSummary(conversationID),
    (signal) => readConversationSummary(conversationID, signal),
    {
      enabled: Boolean(conversationID),
      staleTime: Infinity,
      refetchOnWindowFocus: false,
      // 首次读取完成前用列表中的这一行渲染会话头与输入区。
      placeholder: () => listedConversation(client, conversationID),
    },
  )
  const { data } = resource
  useEffect(() => {
    if (data !== null || !conversationID) return
    queue?.forgetConversation(conversationID)
    outgoingStore.forgetConversation(conversationID)
    clearConversationResources(client, conversationID)
  }, [client, conversationID, data, queue, outgoingStore])
  return resource
}
