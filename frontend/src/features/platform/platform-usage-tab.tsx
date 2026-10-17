/** 平台概览的业务使用页签：按统计周期查看平台整体与各工作区关闭的服务周期、AI 独立解决率、转人工率、真人首响、待补知识和平台模型用量。 */
import { useTranslation } from "react-i18next"

import {
  PlatformUsageSort,
  WorkspaceStatus,
  getPlatformUsage,
  listPlatformWorkspaceUsage,
  type PlatformWorkspaceUsage,
} from "@/api"
import { ListToolbar, ListToolbarFilter } from "@/components/list-toolbar"
// 报表共用 UI 归属 reports；platform 经 public 跨 feature 引用。
import { ReportPeriodFilter, StatTile } from "@/components/metrics"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { AdminListLayout, AdminTableFrame } from "@/features/admin/admin-list-layout"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAdminPageQuery } from "@/hooks/use-admin-page-query"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { periodOptions, useReportFormat } from "@/hooks/use-report-format"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { parseEnum } from "@/lib/enum"

/** 排序方式与对应的选项词条，第一个为默认排序。 */
const sortOptions = [
  [PlatformUsageSort.ServiceSessions, "usage.sorts.serviceSessions"],
  [PlatformUsageSort.Conversations, "usage.sorts.conversations"],
  [PlatformUsageSort.FirstResponse, "usage.sorts.firstResponse"],
  [PlatformUsageSort.KnowledgeGaps, "usage.sorts.knowledgeGaps"],
  [PlatformUsageSort.ModelTokens, "usage.sorts.modelTokens"],
] as const

/** 展示平台整体业务使用指标，并按所选排序列出各工作区的指标。 */
export function PlatformUsageTab() {
  const { t } = useTranslation("platform")
  const { count, compact, rate, duration } = useReportFormat()
  const { searchParams, setParameters } = useListSearchParams()
  const days = periodOptions.find((option) => String(option) === searchParams.get("days")) ?? periodOptions[0]
  const sort = parseEnum(PlatformUsageSort, searchParams.get("sort")) ?? PlatformUsageSort.ServiceSessions
  const invalidate = useResourceInvalidator()
  // 每次进入重新读取；切换周期或排序时保留当前内容直到新数据到达。
  const summary = useResource(resourceKeys.platformUsage({ days }), (signal) => getPlatformUsage({ days }, signal), {
    staleTime: 0,
    keepPreviousData: true,
  })
  const list = useAdminPageQuery(
    resourceKeys.platformWorkspaceUsage({ days, sort }),
    ({ page, pageSize }, signal) => listPlatformWorkspaceUsage({ days, sort, page, pageSize }, signal),
    { staleTime: 0 },
  )
  const total = summary.data

  return (
    <>
      <ListToolbar>
        <ReportPeriodFilter
          value={days}
          onValueChange={(next) => setParameters({ days: next === String(periodOptions[0]) ? null : next, page: null })}
        />
        <ListToolbarFilter
          label={t("usage.sortLabel")}
          value={sort}
          options={sortOptions.map(([value, label]) => ({ value, label: t(label) }))}
          onValueChange={(next) => {
            // 切换排序时同时重读合计，合计与列表取自同一时刻。
            void invalidate(resourceKeys.platformUsage({ days }))
            setParameters({ sort: next === PlatformUsageSort.ServiceSessions ? null : next, page: null })
          }}
        />
      </ListToolbar>

      <AdminListLayout
        resources={[summary, list]}
        errorMessage={t("usage.loadError")}
        pagination={{
          page: list.data?.page,
          onPageChange: list.setPage,
          onPageSizeChange: list.setPageSize,
          busy: list.isPlaceholderData,
        }}
      >
        {total ? (
          <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-3">
            <StatTile
              label={t("usage.serviceSessions")}
              value={count(total.serviceSessions)}
              detail={t("usage.conversationsDetail", { formatted: count(total.conversations) })}
            />
            <StatTile
              label={t("usage.aiResolvedRate")}
              value={rate(total.aiResolved, total.aiClosed)}
              detail={t("usage.aiResolvedDetail", {
                resolved: count(total.aiResolved),
                closed: count(total.aiClosed),
                rate: rate(total.handedOff, total.aiClosed),
              })}
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
            <StatTile
              label={t("usage.modelCalls")}
              value={count(total.modelCalls)}
              detail={t("usage.modelCallsDetail", {
                rate: rate(total.modelCallsFailed, total.modelCallsConcluded),
                failed: count(total.modelCallsFailed),
                concluded: count(total.modelCallsConcluded),
              })}
            />
            <StatTile
              label={t("usage.modelTokens")}
              value={compact(total.inputTokens + total.outputTokens)}
              detail={t("usage.modelTokensDetail", {
                input: compact(total.inputTokens),
                cached: compact(total.cachedInputTokens),
                output: compact(total.outputTokens),
              })}
            />
          </div>
        ) : null}
        <AdminTableFrame>
        <ResourceTable<PlatformWorkspaceUsage>
          columns={[
            {
              key: "workspace",
              header: t("usage.workspaceColumn"),
              cellClassName: "min-w-[8rem] whitespace-nowrap font-medium",
              cell: (item) => item.name,
            },
            {
              key: "status",
              header: t("usage.statusColumn"),
              cellClassName: "w-px whitespace-nowrap",
              cell: (item) => (
                <StatusBadge variant={item.status === WorkspaceStatus.Suspended ? "muted" : "success"}>
                  {t(`workspaces.statuses.${item.status}`)}
                </StatusBadge>
              ),
            },
            {
              key: "serviceSessions",
              header: t("usage.serviceSessions"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (item) => count(item.metrics.serviceSessions),
            },
            {
              key: "conversations",
              header: t("usage.conversationsColumn"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (item) => count(item.metrics.conversations),
            },
            {
              key: "aiResolved",
              header: t("usage.aiResolvedColumn"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (item) => rate(item.metrics.aiResolved, item.metrics.aiClosed),
            },
            {
              key: "handoff",
              header: t("usage.handoffColumn"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (item) => rate(item.metrics.handedOff, item.metrics.aiClosed),
            },
            {
              key: "firstResponse",
              header: t("usage.firstResponse"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (item) => duration(item.metrics.firstResponseMedian),
            },
            {
              key: "modelTokens",
              header: t("usage.modelTokens"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (item) => compact(item.metrics.inputTokens + item.metrics.outputTokens),
            },
            {
              key: "knowledgeGaps",
              header: t("usage.knowledgeGaps"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (item) => count(item.metrics.knowledgeGaps),
            },
          ]}
          rows={list.data?.workspaces ?? []}
          rowKey={(item) => item.id}
          empty={t("usage.empty")}
          showHeader
        />
        </AdminTableFrame>
      </AdminListLayout>
    </>
  )
}
