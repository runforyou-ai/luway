/** 工作区选择页：列出账号已加入的工作区，进入其中之一或在允许时前往创建工作区，已暂停的工作区只展示不可进入；带返回地址进入时可返回原工作区页面。 */
import { useEffect, useState } from "react"
import { ArrowLeftIcon, ChevronRightIcon, LayoutGridIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate, useSearchParams } from "react-router"

import { WorkspaceStatus, listWorkspaces, loadAccount, logout } from "@/api"
import { CountBadge } from "@/components/count-badge"
import { EntryLayout } from "@/components/entry-layout"
import { PageLoading } from "@/components/page-loading"
import { PageLoadError } from "@/components/page-load-error"
import { StatusBadge } from "@/components/status-badge"
import { resourceStatus } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { updateNotificationUnreadIndicator } from "@/platform/notifications"
import { useWorkspaceActivityConnection, useWorkspaceAttention } from "@/hooks/use-workspace-attention"
import { useResource } from "@/hooks/use-resource"
import { resolveServerURL } from "@/lib/server-url"
import { enterWorkspace, navigateToHashPath, returnToPath } from "@/lib/workspace-route"

/** 展示工作区列表；没有工作区时在允许创建时引导创建。 */
export function WorkspaceListPage() {
  const { t } = useTranslation(["account", "common"])
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  // 从工作区内进入时返回原工作区页面，不依赖浏览历史。
  const returnTo = returnToPath(searchParams)
  // 从进不去的工作区地址回到列表时，页头说明原因。
  const unavailable = searchParams.get("unavailable") === "1"
  const account = useResource(resourceKeys.account(), (signal) => loadAccount(signal))
  const workspaces = useResource(resourceKeys.workspaces(), (signal) => listWorkspaces(signal), { staleTime: 0 })
  // 各工作区的未读数量只用于提示，读取失败时不影响进入工作区；停在本页时同样保持工作区动态事件流。
  useWorkspaceActivityConnection("", workspaces.data?.items.map((workspace) => workspace.id) ?? [])
  const workspaceAttention = useWorkspaceAttention("")
  const unreadByWorkspace = workspaceAttention.totals

  // 停在选择页时应用角标合计全部工作区，离开时清除，进入工作区后由工作区外壳接管。
  useEffect(() => {
    if (!workspaceAttention.loaded) return
    void updateNotificationUnreadIndicator({ count: workspaceAttention.others, attentionEnabled: false, attentionPending: false }).catch(
      (error: unknown) => {
        console.warn("同步工作区选择页的应用角标失败", error)
      },
    )
  }, [workspaceAttention.loaded, workspaceAttention.others])
  useEffect(
    () => () => {
      void updateNotificationUnreadIndicator({ count: 0, attentionEnabled: false, attentionPending: false }).catch((error: unknown) => {
        console.warn("清除应用角标失败", error)
      })
    },
    [],
  )
  const serverURL = useResource(resourceKeys.serverURL(), () => resolveServerURL())
  const host = serverURL.data ? new URL(serverURL.data).host : ""
  const [loggingOut, setLoggingOut] = useState(false)

  /** 退出登录后回到登录页。 */
  async function signOut() {
    setLoggingOut(true)
    navigate("/login", { replace: true })
    try {
      await logout()
    } catch (error) {
      console.warn("退出登录失败", error)
    }
  }

  const loadStatus = resourceStatus([account, workspaces])
  if (loadStatus.status === "error") {
    return (
      <PageLoadError
        message={t("loadError")}
        onRetry={() => {
          // 一次重试所有读取失败的资源。
          for (const resource of loadStatus.failed) void resource.refresh()
        }}
      />
    )
  }
  if (!account.data || !workspaces.data) {
    return (
      <PageLoading />
    )
  }

  const canCreate = workspaces.data.canCreate
  const footer = (
    <span className="inline-flex max-w-full items-center gap-1.5">
      <span className="min-w-0 truncate">{t("signedInAs", { email: account.data.email })}</span>
      <span aria-hidden="true">·</span>
      <button
        type="button"
        className="shrink-0 font-medium text-foreground/80 underline-offset-4 hover:text-foreground hover:underline disabled:opacity-50"
        disabled={loggingOut}
        onClick={() => void signOut()}
      >
        {t("logout")}
      </button>
    </span>
  )

  if (workspaces.data.items.length === 0) {
    return (
      <EntryLayout
        title={t("emptyTitle")}
        description={unavailable ? t("workspaceUnavailable") : canCreate ? t("emptyDescription") : t("emptyDescriptionJoin")}
        footer={footer}
      >
        {canCreate ? (
          <div className="flex flex-col items-center rounded-xl border bg-card px-6 py-10 text-center">
            <span className="mb-4 flex size-11 items-center justify-center rounded-full bg-muted text-muted-foreground">
              <LayoutGridIcon className="size-5" />
            </span>
            <Button onClick={() => navigate("/workspaces/new")}>
              <PlusIcon />
              {t("create")}
            </Button>
          </div>
        ) : null}
      </EntryLayout>
    )
  }

  return (
    <EntryLayout
      title={t("title")}
      description={unavailable ? t("workspaceUnavailable") : t("description")}
      footer={footer}
      leading={
        returnTo ? (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="mb-3 -ml-2 text-muted-foreground"
            aria-label={t("common:actions.back")}
            title={t("common:actions.back")}
            onClick={() => navigateToHashPath(returnTo)}
          >
            <ArrowLeftIcon />
          </Button>
        ) : undefined
      }
    >
      <ul className="divide-y overflow-hidden rounded-xl border bg-card">
        {workspaces.data.items.map((workspace) => {
          const suspended = workspace.status === WorkspaceStatus.WorkspaceStatusSuspended
          return (
          <li key={workspace.id}>
            <button
              type="button"
              className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors outline-none hover:bg-muted/60 focus-visible:bg-muted/60 disabled:pointer-events-none disabled:opacity-60"
              disabled={suspended}
              onClick={() => enterWorkspace(workspace.slug)}
            >
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-sm font-semibold text-primary">
                {workspace.name.slice(0, 1).toUpperCase()}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">{workspace.name}</span>
                <span className="block truncate text-xs text-muted-foreground">
                  {host ? `${host}/#/w/${workspace.slug}` : workspace.slug}
                </span>
              </span>
              {suspended ? (
                <StatusBadge variant="muted">{t("workspaceSuspended")}</StatusBadge>
              ) : (
                <>
                  {(unreadByWorkspace.get(workspace.id) ?? 0) > 0 ? (
                    <CountBadge
                      count={unreadByWorkspace.get(workspace.id) ?? 0}
                      label={t("workspaceUnread", { count: unreadByWorkspace.get(workspace.id) ?? 0 })}
                    />
                  ) : null}
                  <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                </>
              )}
            </button>
          </li>
          )
        })}
        {canCreate ? (
          <li>
            <button
              type="button"
              className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm text-muted-foreground transition-colors outline-none hover:bg-muted/60 hover:text-foreground focus-visible:bg-muted/60"
              onClick={() => navigate(returnTo ? `/workspaces/new?via=list&returnTo=${encodeURIComponent(returnTo)}` : "/workspaces/new")}
            >
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-dashed">
                <PlusIcon className="size-4" />
              </span>
              {t("create")}
            </button>
          </li>
        ) : null}
      </ul>
    </EntryLayout>
  )
}
