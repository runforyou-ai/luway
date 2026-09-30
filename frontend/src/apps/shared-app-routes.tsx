/** Web 与桌面端共用的业务路由。 */
import { lazy } from "react"
import { Navigate, Route, Routes } from "react-router"

import { AccountRoutes, WorkspaceRoutes } from "@/apps/account-routes"
import { usePreventPageSelectAll } from "@/hooks/use-prevent-page-select-all"
import { useSessionGeneration } from "@/hooks/use-session-generation"
import { lastWorkspacePath } from "@/lib/workspace-route"

// 工作台与会话独立窗口在首次进入时加载，入口页不携带工作台代码。
const WorkspaceLayout = lazy(() =>
  import("@/features/workspace/workspace-layout").then((module) => ({ default: module.WorkspaceLayout })),
)
const ConversationWindowLayout = lazy(() =>
  import("@/features/workspace/conversation-window-layout").then((module) => ({ default: module.ConversationWindowLayout })),
)

/** 根路径渲染账号级页面；工作区地址下把工作台页面交给标签宿主管理，桌面端另有会话独立窗口路由；登录会话代次变化时重新挂载登录外壳。 */
export function SharedAppRoutes({
  platform,
  workspaceSlug,
}: {
  platform: "web" | "desktop"
  workspaceSlug: string | null
}) {
  usePreventPageSelectAll()
  const sessionGeneration = useSessionGeneration()

  if (!workspaceSlug) {
    return <AccountRoutes platform={platform} />
  }
  return (
    <WorkspaceRoutes slug={workspaceSlug}>
      <Routes>
        {/* 工作区根地址打开本机记住的最近停留页面，没有记录时进入收件箱。 */}
        <Route path="/" element={<Navigate to={lastWorkspacePath(workspaceSlug) ?? "/inbox"} replace />} />
        {platform === "desktop" ? (
          <Route
            path="/conversations/:conversationId"
            element={<ConversationWindowLayout key={sessionGeneration} />}
          />
        ) : null}
        <Route path="*" element={<WorkspaceLayout key={sessionGeneration} />} />
      </Routes>
    </WorkspaceRoutes>
  )
}
