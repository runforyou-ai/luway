/** 桌面端会话独立窗口：登录外壳内只渲染单个会话。 */
import { useParams } from "react-router"

import { WorkspaceProvider } from "@/contexts/workspace-context"
import { ConversationWindowPage } from "@/features/inbox/conversation-window-page"
import { SessionShell } from "@/features/session/session-shell"

/** 在登录外壳内按路由中的会话编号渲染独立窗口页面。 */
export function ConversationWindowLayout() {
  const { conversationId = "" } = useParams()

  return (
    <SessionShell>
      {(identity) => (
        <WorkspaceProvider identity={identity}>
          <div className="app-conversation-window flex h-svh min-h-0 w-full flex-col overflow-hidden bg-background">
            <ConversationWindowPage
              key={conversationId}
              conversationId={conversationId}
            />
          </div>
        </WorkspaceProvider>
      )}
    </SessionShell>
  )
}
