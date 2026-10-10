/** 消息页打开会话时的共用处理。 */
import { useEffect } from "react"

import { preloadConversationMain } from "@/features/inbox/conversation/lazy-conversation-main"
import { useRecentConversations } from "@/features/inbox/shared/use-recent-conversations"

/** 挂载后预取会话主区，打开会话时无需等待下载；已打开的会话记入本机最近打开。 */
export function useOpenedConversation(identityId: string, openedConversationId: string | undefined) {
  const record = useRecentConversations(identityId).record
  useEffect(() => {
    void preloadConversationMain()
  }, [])
  useEffect(() => {
    if (openedConversationId) record(openedConversationId)
  }, [openedConversationId, record])
}
