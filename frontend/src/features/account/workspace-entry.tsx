/** 根路径下的工作区地址：有待处理的连接链接时前往连接页，有待处理的邀请时回到邀请页，有登录前要打开的工作区页面（点击的通知或登录失效时所在的页面）时打开该页面，否则进入最近使用或唯一的工作区并保留页面路径，都没有时前往工作区列表。 */
import { useEffect, useRef } from "react"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate } from "react-router"

import { listWorkspaces } from "@/api"
import { PageLoading } from "@/components/page-loading"
import { PageLoadError } from "@/components/page-load-error"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { takePendingReturnPath } from "@/lib/login-return"
import { invitationPath, takePendingInvitation } from "@/lib/pending-invitation"
import { hasPendingServerLink } from "@/lib/server-link-queue"
import { enterWorkspace, lastWorkspaceSlug, navigateToHashPath, workspaceSlugFromHash } from "@/lib/workspace-route"

/** 读取账号可进入的工作区后决定进入哪个工作区。 */
export function WorkspaceEntry() {
  const { t } = useTranslation(["account", "common"])
  const location = useLocation()
  const navigate = useNavigate()
  const { data, error, retrying, refresh } = useResource(resourceKeys.workspaces(), (signal) => listWorkspaces(signal), { staleTime: 0 })
  // 待处理的邀请与返回页面只取一次，每次挂载只决定一次去向。
  const enteredRef = useRef(false)

  useEffect(() => {
    if (!data || enteredRef.current) return
    enteredRef.current = true
    // 连接链接正在切换服务器，不再进入当前服务器的工作区。
    if (hasPendingServerLink()) {
      navigate("/connect", { replace: true })
      return
    }
    const invitation = location.pathname === "/" ? takePendingInvitation() : null
    if (invitation) {
      navigate(invitationPath(invitation), { replace: true })
      return
    }
    // 登录前打开的工作区页面（点击的通知、过期时所在的页面）在登录后打开。
    const returnPath = location.pathname === "/" ? takePendingReturnPath() : null
    if (returnPath && workspaceSlugFromHash(returnPath)) {
      navigateToHashPath(returnPath, { replace: true })
      return
    }
    const lastSlug = lastWorkspaceSlug()
    const target =
      data.items.find((workspace) => workspace.slug === lastSlug) ??
      (data.items.length === 1 ? data.items[0] : undefined)
    if (!target) {
      navigate("/workspaces", { replace: true })
      return
    }
    const path = location.pathname === "/" ? undefined : `${location.pathname}${location.search}`
    enterWorkspace(target.slug, path, { replace: true })
  }, [data, location.pathname, location.search, navigate])

  if (error && !data && !retrying) {
    return <PageLoadError message={t("account:loadError")} onRetry={refresh} />
  }
  return (
    <PageLoading />
  )
}
