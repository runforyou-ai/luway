/** 桌面端独立窗口中的单个会话页面，窗口标题跟随会话名称。 */
import { useEffect } from "react"
import { Window } from "@wailsio/runtime"

import { ConversationDetail } from "@/features/inbox/conversation-detail"
import { preloadConversationMain } from "@/features/inbox/lazy-conversation-main"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useConversationSummary } from "@/features/inbox/use-conversation-summary"

// 独立窗口路由加载时即下载会话主区，与身份和摘要读取并行。
void preloadConversationMain()

/** 读取会话摘要并渲染会话详情，退群后关闭本窗口。 */
export function ConversationWindowPage({
  conversationId,
}: {
  conversationId: string
}) {
  const conversationName = useConversationName()
  const summary = useConversationSummary(conversationId)
  const title = summary.data ? conversationName(summary.data) : ""

  useEffect(() => {
    if (!title) return
    document.title = title
    void Window.SetTitle(title).catch((error: unknown) => {
      console.warn("更新会话窗口标题失败", { conversationId, error })
    })
  }, [conversationId, title])

  return (
    <ConversationDetail
      summary={summary}
      onGroupLeft={() => {
        void Window.Close().catch((error: unknown) => {
          console.warn("关闭会话窗口失败", { conversationId, error })
        })
      }}
    />
  )
}
