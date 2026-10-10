/** 清理失权会话的共享读取资源，阻止在途查询重新安装旧结果。 */
import type { QueryClient } from "@tanstack/react-query"
import { resourceKeys } from "@/hooks/resource-keys"
import type { InboxConversationResults } from "@/api"

/** 保留独立摘要的不可用结果，移除正文及衍生缓存并重读列表。 */
export function clearConversationResources(client: QueryClient, conversationID: string) {
  const keys = [
    resourceKeys.conversationMessages(conversationID),
    resourceKeys.conversationMessagePage(conversationID),
    resourceKeys.conversationMessageContext(conversationID),
    resourceKeys.conversationNavigation(conversationID),
    resourceKeys.conversationMentions(conversationID),
    resourceKeys.groupConversation(conversationID),
    resourceKeys.serviceBusinessQueries(conversationID),
    resourceKeys.serviceSummaries(conversationID),
  ]
  for (const queryKey of keys) client.removeQueries({ queryKey })
  client.removeQueries({ queryKey: resourceKeys.attachmentDownload(conversationID) })
  client.removeQueries({ queryKey: resourceKeys.directConversation() })
  // 正在展示的批次由列表立即隐藏失权行，权威重读接替后再释放其余摘要。
  client.removeQueries({ queryKey: resourceKeys.inboxConversations(), predicate: (query) => {
    const data = query.state.data as InboxConversationResults | undefined
    return query.getObserversCount() === 0 && (!data?.results || Boolean(data.results.some((row) => row.id === conversationID && row.conversation)))
  } })
  void client.invalidateQueries({ queryKey: resourceKeys.inboxConversations(), refetchType: "none" })
  client.removeQueries({ queryKey: resourceKeys.inboxContext() })
  client.removeQueries({ queryKey: resourceKeys.inboxWindow() })
  // 由窗口控制器串行重读首页并完成失权恢复。
  void client.resetQueries({ queryKey: resourceKeys.inbox(), predicate: (query) => query.isActive() || query.getObserversCount() === 0 })
  void client.invalidateQueries({ queryKey: resourceKeys.inbox(), refetchType: "none" })
}
