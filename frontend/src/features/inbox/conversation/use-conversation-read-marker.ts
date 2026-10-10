/** 进入会话时清除手动未读标记，并保存当前用户实际看到的最新消息。 */
import { useCallback, useEffect, useEffectEvent } from "react"
import { useNavigate } from "react-router"

import { markConversationRead, updateConversationUnreadMark } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { recoverSession } from "@/lib/session-navigation"

/** 会话激活时清除未读标记，返回单调推进已读水位的回调。 */
export function useConversationReadMarker(conversationID: string, active: boolean) {
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()

  /** 清除未读标记失败时记录日志并恢复会话入口。 */
  const handleUnreadMarkClearError = useEffectEvent(
    (id: string, error: unknown) => {
      console.warn("清除会话未读标记失败", { conversationId: id, error })
      recoverSession(error, navigate)
    },
  )

  useEffect(() => {
    if (!conversationID || !active) return
    let current = true
    // 每次进入会话时清除服务端未读标记。
    void updateConversationUnreadMark(conversationID, { markedUnread: false })
      .then(() => invalidate(resourceKeys.inbox()))
      .catch((error: unknown) => {
        if (!current) return
        handleUnreadMarkClearError(conversationID, error)
      })
    return () => {
      current = false
    }
  }, [conversationID, active, invalidate])

  return useCallback(
    (messageID: string) => {
      // 保存已看到的最新消息并刷新收件箱未读摘要。
      void markConversationRead(conversationID, {
        lastReadMessageId: messageID,
        clearUnreadMark: false,
      })
        .then(() => {
          void invalidate(resourceKeys.inbox())
          void invalidate(resourceKeys.conversationSummary(conversationID))
        })
        .catch((error: unknown) =>
          console.warn("标记会话已读失败", {
            conversationId: conversationID,
            error,
          }),
        )
    },
    [conversationID, invalidate],
  )
}
