/** 工作区入口：按地址中的工作区标识确定请求目标工作区，账号不在该工作区或工作区已暂停时回到工作区列表。 */
import { useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useLocation } from "react-router"

import { WorkspaceStatus, listWorkspaces } from "@/api"
import { setRequestWorkspace } from "@/api/session-scope"
import { PageLoadError } from "@/components/page-load-error"
import { PageLoading } from "@/components/page-loading"
import { WorkspaceScopeProvider } from "@/contexts/workspace-scope-context"
import { useLocalComputerRegistration } from "@/features/workspace/use-local-computer-registration"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { navigateToHashPath, rememberWorkspacePath, rememberWorkspaceSlug } from "@/lib/workspace-route"

/** 找到地址对应的工作区后设置请求目标并渲染工作区页面。 */
export function WorkspaceGate({ slug, children }: { slug: string; children: ReactNode }) {
  const { t } = useTranslation(["account", "common"])
  const { data, error, retrying, refresh } = useResource(resourceKeys.workspaces(), (signal) => listWorkspaces(signal))
  // 已暂停的工作区不能进入，也不出现在工作区切换中。
  const enterable = useMemo(() => data?.items.filter((item) => item.status === WorkspaceStatus.Active), [data])
  const workspace = enterable?.find((item) => item.slug === slug)
  useLocalComputerRegistration(useMemo(() => data?.items.map((item) => item.id), [data]))
  const [readyID, setReadyID] = useState("")
  const scope = useMemo(
    () => (workspace && data && enterable ? { current: workspace, workspaces: enterable, canCreate: data.canCreate } : null),
    [workspace, data, enterable],
  )

  // 工作区确定后先设置请求目标，再渲染发起请求的页面。
  useLayoutEffect(() => {
    if (!workspace) return
    setRequestWorkspace(workspace.id)
    rememberWorkspaceSlug(workspace.slug)
    setReadyID(workspace.id)
    return () => setRequestWorkspace("")
  }, [workspace])

  // 记住本工作区最近停留的页面，工作区根地址与会话独立窗口的地址不计入。
  const location = useLocation()
  useEffect(() => {
    if (!workspace || location.pathname === "/" || location.pathname.startsWith("/conversations/")) return
    rememberWorkspacePath(workspace.slug, `${location.pathname}${location.search}`)
  }, [workspace, location.pathname, location.search])

  // 缓存的工作区列表可能早于刚创建或刚加入的工作区，找不到时先重读：重读成功仍找不到才回到工作区列表并由列表页说明原因，重读失败时提供重试。
  const [verification, setVerification] = useState<{ slug: string; status: "verifying" | "verified" | "failed" } | null>(null)
  const verificationStatus = verification?.slug === slug ? verification.status : null
  useEffect(() => {
    if (!data || workspace) return
    if (verificationStatus === null) {
      setVerification({ slug, status: "verifying" })
      void refresh().then((result) => setVerification({ slug, status: result.isSuccess ? "verified" : "failed" }))
      return
    }
    if (verificationStatus !== "verified") return
    console.info("地址中的工作区不可进入", { slug })
    navigateToHashPath("/workspaces?unavailable=1", { replace: true })
  }, [data, workspace, slug, verificationStatus, refresh])

  if (error && !data && !retrying) {
    return <PageLoadError message={t("account:loadError")} onRetry={refresh} />
  }
  if (!workspace && verificationStatus === "failed") {
    return <PageLoadError message={t("account:loadError")} onRetry={() => setVerification(null)} />
  }
  if (!scope || readyID !== scope.current.id) {
    return (
      <PageLoading />
    )
  }
  return (
    <WorkspaceScopeProvider value={scope}>
      {children}
    </WorkspaceScopeProvider>
  )
}
