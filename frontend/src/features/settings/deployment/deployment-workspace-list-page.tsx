/** 部署设置的全部工作区页：按状态筛选、排序和搜索部署内的全部工作区，查看规模与最近活跃，暂停或恢复工作区。 */
import { LayoutGridIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  DeploymentWorkspaceSort,
  WorkspaceStatus,
  getDeploymentSettings,
  listDeploymentWorkspaces,
  resumeDeploymentWorkspace,
  suspendDeploymentWorkspace,
  type DeploymentWorkspace,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbar, ListToolbarFilter, ListToolbarSearch, ListToolbarTotal } from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable, type ResourceRowAction } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { usePagedResource, useResource } from "@/hooks/use-resource"
import { formatFileSize } from "@/lib/file-size"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 工作区行上需要确认的状态修改。 */
type WorkspaceChange = "suspend" | "resume"

/** 待确认的工作区与修改。 */
type PendingWorkspaceChange = {
  workspace: DeploymentWorkspace
  change: WorkspaceChange
}

/** 各修改对应的请求。 */
const workspaceChangeRequests: Record<WorkspaceChange, (workspaceId: string) => Promise<DeploymentWorkspace>> = {
  suspend: suspendDeploymentWorkspace,
  resume: resumeDeploymentWorkspace,
}

/** 排序方式与对应的选项词条，第一个为默认排序。 */
const sortOptions = [
  [DeploymentWorkspaceSort.DeploymentWorkspaceSortCreatedAt, "workspaces.sorts.createdAt"],
  [DeploymentWorkspaceSort.DeploymentWorkspaceSortLastActive, "workspaces.sorts.lastActive"],
  [DeploymentWorkspaceSort.DeploymentWorkspaceSortMemberCount, "workspaces.sorts.memberCount"],
  [DeploymentWorkspaceSort.DeploymentWorkspaceSortStorage, "workspaces.sorts.storage"],
] as const

/** 返回统计日期距统计时区今天的天数，日期为 YYYY-MM-DD。 */
function daysSince(date: string, timeZone: string) {
  const today = new Intl.DateTimeFormat("en-CA", { timeZone, year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date())
  return Math.max(0, Math.round((Date.parse(`${today}T00:00:00Z`) - Date.parse(`${date}T00:00:00Z`)) / 86_400_000))
}

/** 列出部署工作区；暂停与恢复经确认后执行，已暂停的工作区在名称旁标记。 */
export function DeploymentWorkspaceListPage() {
  const { t } = useTranslation("deployment")
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams()
  const status = optionalWailsEnum(WorkspaceStatus, searchParams.get("status")) ?? WorkspaceStatus.$zero
  const sort = optionalWailsEnum(DeploymentWorkspaceSort, searchParams.get("sort")) ?? DeploymentWorkspaceSort.DeploymentWorkspaceSortCreatedAt
  const list = usePagedResource(
    resourceKeys.deploymentWorkspaces({ query, status, sort, pageSize: 50 }),
    (page, signal) => listDeploymentWorkspaces({ query, status, sort, page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.workspaces, page: data.page }), itemKey: (item) => item.id },
  )
  // 最近活跃日期按统计时区划分，相对天数同样按统计时区的今天计算。
  const settings = useResource(resourceKeys.deploymentSettings(), (signal) => getDeploymentSettings(signal))
  const pending = useConfirmedAction<PendingWorkspaceChange>({
    action: ({ workspace, change }) => workspaceChangeRequests[change](workspace.id),
    invalidateKeys: () => [
      resourceKeys.deploymentWorkspaces(),
      resourceKeys.deploymentWorkspaceUsage(),
      resourceKeys.workspaces(),
    ],
    successMessage: ({ change }) => t(change === "suspend" ? "workspaces.suspended" : "workspaces.resumed"),
    errorMessage: () => t("workspaces.updateError"),
    logLabel: "修改工作区状态",
  })

  /** 正常工作区可暂停，有部署管理员成员时暂停不可用；已暂停的工作区可恢复。 */
  function rowActions(item: DeploymentWorkspace): ResourceRowAction[] {
    const change: WorkspaceChange = item.status === WorkspaceStatus.WorkspaceStatusSuspended ? "resume" : "suspend"
    return [
      {
        key: change,
        label: change === "suspend" && item.hasDeploymentAdmin ? t("workspaces.suspendUnavailable") : t(`workspaces.${change}`),
        onSelect: () => pending.select({ workspace: item, change }),
        disabled: change === "suspend" && item.hasDeploymentAdmin,
        destructive: change === "suspend",
      },
    ]
  }

  /** 返回最近活跃的说明，从未活跃时说明尚无活跃；统计时区读取前按 UTC 计算。 */
  function lastActive(item: DeploymentWorkspace) {
    if (!item.lastActiveOn) return t("workspaces.neverActive")
    const days = daysSince(item.lastActiveOn, settings.data?.statisticsTimeZone ?? "UTC")
    return days === 0 ? t("workspaces.activeToday") : t("workspaces.activeDaysAgo", { count: days })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("workspaces.title")} description={t("workspaces.description")} />

      <ListToolbar>
        <ListToolbarSearch value={search} aria-label={t("workspaces.search")} onChange={(event) => setSearch(event.target.value)} />
        <ListToolbarFilter
          label={t("workspaces.statusFilter")}
          allLabel={t("workspaces.allStatuses")}
          value={status}
          options={[
            { value: WorkspaceStatus.WorkspaceStatusActive, label: t("workspaces.statuses.active") },
            { value: WorkspaceStatus.WorkspaceStatusSuspended, label: t("workspaces.statuses.suspended") },
          ]}
          onValueChange={(next) => setParameters({ status: next || null })}
        />
        <ListToolbarFilter
          label={t("workspaces.sortLabel")}
          value={sort}
          options={sortOptions.map(([value, label]) => ({ value, label: t(label) }))}
          onValueChange={(next) => setParameters({ sort: next === DeploymentWorkspaceSort.DeploymentWorkspaceSortCreatedAt ? null : next })}
        />
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout resources={list} errorMessage={t("workspaces.loadError")} more={list.more}>
        <ResourceTable<DeploymentWorkspace>
          columns={[
            {
              key: "workspace",
              header: t("workspaces.title"),
              cellClassName: "min-w-0",
              cell: (item) => (
                <ResourceRowIdentity
                  icon={LayoutGridIcon}
                  name={item.name}
                  secondary={t("workspaces.memberCount", { count: item.memberCount })}
                  badge={
                    item.status === WorkspaceStatus.WorkspaceStatusSuspended ? (
                      <StatusBadge variant="muted">{t("workspaces.statuses.suspended")}</StatusBadge>
                    ) : undefined
                  }
                  description={t("workspaces.scale", {
                    agents: item.aiEmployeeCount,
                    channels: item.channelCount,
                    devices: item.deviceCount,
                  })}
                />
              ),
            },
            {
              key: "storage",
              header: t("workspaces.storageColumn"),
              cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground tabular-nums sm:table-cell",
              cell: (item) => formatFileSize(item.storageBytes),
            },
            {
              key: "active",
              header: t("workspaces.lastActiveColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: lastActive,
            },
          ]}
          rows={list.data?.items ?? []}
          rowKey={(item) => item.id}
          empty={t("workspaces.empty")}
          rowActions={rowActions}
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...pending.dialog}
        destructive={pending.item?.change === "suspend"}
        title={pending.item ? t(`workspaces.${pending.item.change}Title`, { name: pending.item.workspace.name }) : ""}
        description={pending.item ? t(`workspaces.${pending.item.change}Description`) : ""}
        pendingLabel={t("workspaces.saving")}
      />
    </div>
  )
}
