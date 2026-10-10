/** 工作区与账号的工作区页签：按状态筛选、排序和搜索平台内的全部工作区，查看规模与最近活跃，追加构建挂接的行操作，暂停或恢复工作区。 */
import { LayoutGridIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  PlatformWorkspaceSort,
  WorkspaceStatus,
  listPlatformWorkspaces,
  resumePlatformWorkspace,
  suspendPlatformWorkspace,
  type PlatformWorkspace,
} from "@/api"
import { appExtensions } from "@/app-extensions"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbar, ListToolbarFilter, ListToolbarSearch, ListToolbarTotal } from "@/components/list-toolbar"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable, type ResourceRowAction } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { WorkspaceAddress } from "@/components/workspace-address"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { usePagedResource } from "@/hooks/use-resource"
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

/** 列出平台工作区与行操作，暂停与恢复经确认后执行，已暂停的工作区在名称旁标记。 */
export function PlatformWorkspaceListTab() {
  const { t } = useTranslation("platform")
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams()
  const status = parseEnum(WorkspaceStatus, searchParams.get("status"))
  const extensionActions = appExtensions().usePlatformWorkspaceActions()
  const sort = parseEnum(PlatformWorkspaceSort, searchParams.get("sort")) ?? PlatformWorkspaceSort.CreatedAt
  const list = usePagedResource(
    resourceKeys.platformWorkspaces({ query, status, sort, pageSize: 50 }),
    (page, signal) => listPlatformWorkspaces({ query, status, sort, page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.workspaces, page: data.page }), itemKey: (item) => item.id },
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

  /** 追加构建挂接的操作；正常工作区可暂停，有平台管理员成员时暂停不可用；已暂停的工作区可恢复。 */
  function rowActions(item: PlatformWorkspace): ResourceRowAction[] {
    const change: WorkspaceChange = item.status === WorkspaceStatus.Suspended ? "resume" : "suspend"
    return [
      ...extensionActions.rowActions(item),
      {
        key: change,
        separatorBefore: true,
        label: change === "suspend" && item.hasPlatformAdmin ? t("workspaces.suspendUnavailable") : t(`workspaces.${change}`),
        onSelect: () => pending.select({ workspace: item, change }),
        disabled: change === "suspend" && item.hasPlatformAdmin,
        destructive: change === "suspend",
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
          onValueChange={(next) => setParameters({ status: next || null })}
        />
        <ListToolbarFilter
          label={t("workspaces.sortLabel")}
          value={sort}
          options={sortOptions.map(([value, label]) => ({ value, label: t(label) }))}
          onValueChange={(next) => setParameters({ sort: next === PlatformWorkspaceSort.CreatedAt ? null : next })}
        />
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout resources={list} errorMessage={t("workspaces.loadError")} more={list.more}>
        <ResourceTable<PlatformWorkspace>
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
                    item.status === WorkspaceStatus.Suspended ? (
                      <StatusBadge variant="muted">{t("workspaces.statuses.suspended")}</StatusBadge>
                    ) : undefined
                  }
                  description={t("workspaces.scale", {
                    agents: item.aiEmployeeCount,
                    channels: item.channelCount,
                    computers: item.computerCount,
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
