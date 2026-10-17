/** 工作区管理列表：按状态筛选、排序和搜索平台内的全部工作区并按页码翻页，查看规模与最近活跃，追加构建挂接的行操作，暂停或恢复工作区。 */
import { useTranslation } from "react-i18next"

import {
  PlatformWorkspaceSort,
  WorkspaceStatus,
  createSupportAccess,
  listPlatformWorkspaces,
  resumePlatformWorkspace,
  suspendPlatformWorkspace,
  type Account,
  type Auth,
  type PlatformWorkspace,
} from "@/api"
import { storeToken } from "@/api/client"
import { beginSessionBoundary } from "@/lib/resource-client"
import { enterWorkspace } from "@/lib/workspace-route"
import { toast } from "sonner"
import { appExtensions } from "@/app-extensions"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbar, ListToolbarFilter, ListToolbarSearch } from "@/components/list-toolbar"
import { ResourceTable, type ResourceRowAction } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { AdminListLayout, AdminTableFrame } from "@/features/admin/admin-list-layout"
import { WorkspaceAddress } from "@/components/workspace-address"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useAdminPageQuery } from "@/hooks/use-admin-page-query"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { parseEnum } from "@/lib/enum"
import { formatFileSize } from "@/lib/file-size"

/** 工作区行上需要确认的状态修改。 */
type WorkspaceChange = "suspend" | "resume"

/** 待确认的工作区与修改。 */
type PendingWorkspaceChange = {
  workspace: PlatformWorkspace
  change: WorkspaceChange
}

/** 各修改对应的请求。 */
const workspaceChangeRequests: Record<WorkspaceChange, (workspaceId: string) => Promise<PlatformWorkspace>> = {
  suspend: suspendPlatformWorkspace,
  resume: resumePlatformWorkspace,
}

/** 排序方式与对应的选项词条，第一个为默认排序。 */
const sortOptions = [
  [PlatformWorkspaceSort.CreatedAt, "workspaces.sorts.createdAt"],
  [PlatformWorkspaceSort.LastActive, "workspaces.sorts.lastActive"],
  [PlatformWorkspaceSort.MemberCount, "workspaces.sorts.memberCount"],
  [PlatformWorkspaceSort.Storage, "workspaces.sorts.storage"],
] as const

/** 列出平台工作区与行操作，暂停与恢复经确认后执行，已暂停的工作区在状态列标记。 */
export function PlatformWorkspaceList({ account }: { account: Account }) {
  const { t } = useTranslation("platform")
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams({ resetParameter: "page" })
  const status = parseEnum(WorkspaceStatus, searchParams.get("status"))
  const extensionActions = appExtensions().usePlatformWorkspaceActions()
  const sort = parseEnum(PlatformWorkspaceSort, searchParams.get("sort")) ?? PlatformWorkspaceSort.CreatedAt
  const list = useAdminPageQuery(
    resourceKeys.platformWorkspaces({ query, status, sort }),
    ({ page, pageSize }, signal) => listPlatformWorkspaces({ query, status, sort, page, pageSize }, signal),
  )
  const pending = useConfirmedAction<PendingWorkspaceChange>({
    action: ({ workspace, change }) => workspaceChangeRequests[change](workspace.id),
    invalidateKeys: () => [
      resourceKeys.platformWorkspaces(),
      resourceKeys.platformWorkspaceUsage(),
      resourceKeys.workspaces(),
    ],
    successMessage: ({ change }) => t(change === "suspend" ? "workspaces.suspended" : "workspaces.resumed"),
    errorMessage: () => t("workspaces.updateError"),
    logLabel: "修改工作区状态",
  })

  /** 进入支援模式：签发短期用户端凭证并打开该工作区。 */
  async function enterSupport(workspace: PlatformWorkspace) {
    try {
      const access = await createSupportAccess(workspace.id)
      const auth: Auth = {
        account,
        token: access.token,
        expiresAt: access.expiresAt,
      }
      storeToken(auth)
      beginSessionBoundary()
      enterWorkspace(access.workspace.slug, "/inbox")
    } catch (error) {
      console.warn("签发支援会话失败", error)
      toast.error(t("workspaces.supportError"))
    }
  }

  /** 进入与挂接的常用操作放在操作列，暂停放进「⋯」；恢复作为常用操作直接显示。 */
  function rowActions(item: PlatformWorkspace): ResourceRowAction[] {
    const change: WorkspaceChange = item.status === WorkspaceStatus.Suspended ? "resume" : "suspend"
    return [
      {
        key: "support",
        label: t("workspaces.supportEnter"),
        primary: true,
        onSelect: () => void enterSupport(item),
        disabled: item.status === WorkspaceStatus.Suspended,
      },
      ...extensionActions.rowActions(item).map((action) => ({ ...action, primary: true })),
      {
        key: change,
        label: change === "suspend" && item.hasPlatformAdmin ? t("workspaces.suspendUnavailable") : t(`workspaces.${change}`),
        onSelect: () => pending.select({ workspace: item, change }),
        disabled: change === "suspend" && item.hasPlatformAdmin,
        destructive: change === "suspend",
        primary: change === "resume",
      },
    ]
  }

  /** 返回最近活跃的说明，从未活跃时说明尚无活跃。 */
  function lastActive(item: PlatformWorkspace) {
    if (item.lastActiveDays == null) return t("workspaces.neverActive")
    return item.lastActiveDays === 0 ? t("workspaces.activeToday") : t("workspaces.activeDaysAgo", { count: item.lastActiveDays })
  }

  return (
    <>
      <ListToolbar>
        <ListToolbarSearch value={search} aria-label={t("workspaces.search")} onChange={(event) => setSearch(event.target.value)} />
        <ListToolbarFilter
          label={t("workspaces.statusFilter")}
          allLabel={t("workspaces.allStatuses")}
          value={status}
          options={[
            { value: WorkspaceStatus.Active, label: t("workspaces.statuses.active") },
            { value: WorkspaceStatus.Suspended, label: t("workspaces.statuses.suspended") },
          ]}
          onValueChange={(next) => setParameters({ status: next || null, page: null })}
        />
        <ListToolbarFilter
          label={t("workspaces.sortLabel")}
          value={sort}
          options={sortOptions.map(([value, label]) => ({ value, label: t(label) }))}
          onValueChange={(next) => setParameters({ sort: next === PlatformWorkspaceSort.CreatedAt ? null : next, page: null })}
        />
      </ListToolbar>

      <AdminListLayout
        resources={list}
        errorMessage={t("workspaces.loadError")}
        pagination={{
          page: list.data?.page,
          onPageChange: list.setPage,
          onPageSizeChange: list.setPageSize,
          busy: list.isPlaceholderData,
        }}
      >
        <AdminTableFrame>
          <ResourceTable<PlatformWorkspace>
            columns={[
              {
                key: "name",
                header: t("workspaces.nameColumn"),
                cellClassName: "min-w-[8rem] whitespace-nowrap font-medium",
                cell: (item) => item.name,
              },
              {
                key: "address",
                header: t("workspaces.addressColumn"),
                cellClassName: "min-w-[10rem] whitespace-nowrap",
                cell: (item) => <WorkspaceAddress slug={item.slug} />,
              },
              {
                key: "status",
                header: t("workspaces.statusColumn"),
                cellClassName: "w-px whitespace-nowrap",
                cell: (item) => (
                  <StatusBadge variant={item.status === WorkspaceStatus.Suspended ? "muted" : "success"}>
                    {t(`workspaces.statuses.${item.status}`)}
                  </StatusBadge>
                ),
              },
              {
                key: "members",
                header: t("workspaces.membersColumn"),
                cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
                cell: (item) => item.memberCount,
              },
              {
                key: "agents",
                header: t("workspaces.agentsColumn"),
                cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
                cell: (item) => item.aiEmployeeCount,
              },
              {
                key: "channels",
                header: t("workspaces.channelsColumn"),
                cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
                cell: (item) => item.channelCount,
              },
              {
                key: "computers",
                header: t("workspaces.computersColumn"),
                cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
                cell: (item) => item.computerCount,
              },
              {
                key: "storage",
                header: t("workspaces.storageColumn"),
                cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
                cell: (item) => formatFileSize(item.storageBytes),
              },
              {
                key: "active",
                header: t("workspaces.lastActiveColumn"),
                cellClassName: "w-px whitespace-nowrap text-muted-foreground",
                cell: lastActive,
              },
            ]}
            rows={list.data?.workspaces ?? []}
            rowKey={(item) => item.id}
            empty={t("workspaces.empty")}
            rowActions={rowActions}
            showHeader
          />
        </AdminTableFrame>
      </AdminListLayout>

      {extensionActions.dialogs}

      <ConfirmationDialog
        {...pending.dialog}
        destructive={pending.item?.change === "suspend"}
        title={pending.item ? t(`workspaces.${pending.item.change}Title`, { name: pending.item.workspace.name }) : ""}
        description={pending.item ? <>
          <WorkspaceAddress slug={pending.item.workspace.slug} className="mb-2" />
          {t(`workspaces.${pending.item.change}Description`)}
        </> : ""}
        pendingLabel={t("workspaces.saving")}
      />
    </>
  )
}
