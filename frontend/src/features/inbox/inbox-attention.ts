/** 应用角标、导航与页签数量共用的提醒读取。 */
import { InboxScope, loadInbox, type Identity } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 读取聊天提醒未读数与本人待处理的服务会话数，total 为应用角标数，由聊天提醒未读数与待处理会话中的未读消息数相加。 */
export async function loadInboxAttention() {
  const inbox = await loadInbox({ scope: InboxScope.InboxScopeChat, limit: 1 })
  return {
    unread: inbox.attentionUnreadCount,
    pending: inbox.pendingCount,
    total: inbox.attentionUnreadCount + inbox.pendingUnreadCount,
  }
}

/** 按当前身份读取提醒数量，同一身份的各处共用一份缓存，会话变化由同步协调器失效。 */
export function useInboxAttention(identity: Identity) {
  return useResource(
    resourceKeys.inboxAttention({ organizationId: identity.organization.id, userId: identity.user.id }),
    loadInboxAttention,
  )
}
