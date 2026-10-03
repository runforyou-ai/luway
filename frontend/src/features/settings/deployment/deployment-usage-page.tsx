/** 部署设置的业务使用页：按统计周期查看部署整体与各工作区的客服接待量、AI 独立解决率、转人工率、真人首响和待补知识。 */
import { LayoutGridIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  DeploymentUsageSort,
  WorkspaceStatus,
  getDeploymentUsage,
  listDeploymentWorkspaceUsage,
  type DeploymentWorkspaceUsage,
} from "@/api"
import { ListToolbar, ListToolbarFilter, ListToolbarTotal } from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ReportPeriodFilter, StatTile } from "@/components/report-parts"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { resourceKeys } from "@/hooks/resource-keys"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { periodOptions, useReportFormat } from "@/hooks/use-report-format"
import { usePagedResource, useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 排序方式与对应的选项词条，第一个为默认排序。 */
const sortOptions = [
  [DeploymentUsageSort.DeploymentUsageSortServiceSessions, "usage.sorts.serviceSessions"],
  [DeploymentUsageSort.DeploymentUsageSortConversations, "usage.sorts.conversations"],
  [DeploymentUsageSort.DeploymentUsageSortFirstResponse, "usage.sorts.firstResponse"],
  [DeploymentUsageSort.DeploymentUsageSortKnowledgeGaps, "usage.sorts.knowledgeGaps"],
] as const

/** 展示部署整体业务使用指标，并按所选排序列出各工作区的指标。 */
export function DeploymentUsagePage() {
  const { t } = useTranslation("deployment")
  const { count, rate, duration } = useReportFormat()
  const { searchParams, setParameters } = useListSearchParams()
  const days = periodOptions.find((option) => String(option) === searchParams.get("days")) ?? periodOptions[0]
  const sort = optionalWailsEnum(DeploymentUsageSort, searchParams.get("sort")) ?? DeploymentUsageSort.DeploymentUsageSortServiceSessions
  const invalidate = useResourceInvalidator()
  // 每次进入重新读取；切换周期或排序时保留当前内容直到新数据到达。
  const summary = useResource(resourceKeys.deploymentUsage({ days }), (signal) => getDeploymentUsage({ days }, signal), {
    staleTime: 0,
    keepPreviousData: true,
  })
  const list = usePagedResource(
    resourceKeys.deploymentWorkspaceUsage({ days, sort, pageSize: 50 }),
    (page, signal) => listDeploymentWorkspaceUsage({ days, sort, page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.workspaces, page: data.page }), itemKey: (item) => item.id, staleTime: 0, keepPreviousData: true },
  )
  const total = summary.data

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("usage.title")} description={t("usage.description")} />

      <ListToolbar>
        <ReportPeriodFilter
          value={days}
          onValueChange={(next) => setParameters({ days: next === String(periodOptions[0]) ? null : next })}
        />
        <ListToolbarFilter
          label={t("usage.sortLabel")}
          value={sort}
          options={sortOptions.map(([value, label]) => ({ value, label: t(label) }))}
          onValueChange={(next) => {
            // 切换排序时同时重读合计，合计与列表取自同一时刻。
            void invalidate(resourceKeys.deploymentUsage({ days }))
            setParameters({ sort: next === DeploymentUsageSort.DeploymentUsageSortServiceSessions ? null : next })
          }}
        />
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout resources={[summary, list]} errorMessage={t("usage.loadError")} more={list.more}>
        {total ? (
          <div className="mx-3 mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
            <StatTile
              label={t("usage.serviceSessions")}
              value={count(total.serviceSessions)}
              detail={t("usage.conversationsDetail", { formatted: count(total.conversations) })}
            />
            <StatTile
              label={t("usage.aiResolvedRate")}
              value={rate(total.aiResolved, total.aiClosed)}
              detail={t("usage.handoffRateDetail", { rate: rate(total.handedOff, total.aiClosed) })}
            />
            <StatTile
              label={t("usage.firstResponse")}
              value={duration(total.firstResponseMedian)}
              detail={
                total.firstResponseP90 === null
                  ? t("usage.noFirstResponse")
                  : t("usage.firstResponseP90", { duration: duration(total.firstResponseP90) })
              }
            />
            <StatTile
              label={t("usage.knowledgeGaps")}
              value={count(total.knowledgeGaps)}
              detail={t("usage.knowledgeGapsDetail")}
            />
          </div>
        ) : null}
        <ResourceTable<DeploymentWorkspaceUsage>
          columns={[
            {
              key: "workspace",
              header: t("usage.workspaceColumn"),
              cellClassName: "min-w-0",
              cell: (item) => (
                <ResourceRowIdentity
                  icon={LayoutGridIcon}
                  name={item.name}
                  secondary={t("usage.workspaceVolume", {
                    sessions: count(item.metrics.serviceSessions),
                    conversations: count(item.metrics.conversations),
                  })}
                  badge={
                    item.status === WorkspaceStatus.WorkspaceStatusSuspended ? (
                      <StatusBadge variant="muted">{t("workspaces.statuses.suspended")}</StatusBadge>
                    ) : undefined
                  }
                  description={t("usage.workspaceRates", {
                    resolved: rate(item.metrics.aiResolved, item.metrics.aiClosed),
                    handoff: rate(item.metrics.handedOff, item.metrics.aiClosed),
                  })}
                />
              ),
            },
            {
              key: "firstResponse",
              header: t("usage.firstResponse"),
              cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground tabular-nums sm:table-cell",
              cell: (item) => t("usage.firstResponseCell", { duration: duration(item.metrics.firstResponseMedian) }),
            },
            {
              key: "knowledgeGaps",
              header: t("usage.knowledgeGaps"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
              cell: (item) => t("usage.knowledgeGapsCell", { count: item.metrics.knowledgeGaps }),
            },
          ]}
          rows={list.data?.items ?? []}
          rowKey={(item) => item.id}
          empty={t("usage.empty")}
        />
      </ResourceListLayout>
    </div>
  )
}
