/** 团队表现报表的滚动加载列表：按客服、按渠道或咨询分类拆分与真人接待的问题会话。 */
import { useTranslation } from "react-i18next"

import {
  ServiceIssueType,
  listTeamPerformanceBreakdowns,
  listTeamPerformanceIssues,
  listTeamPerformanceMembers,
  type ServiceReportDimension,
} from "@/api"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { resourceKeys } from "@/hooks/resource-keys"
import { usePagedResource } from "@/hooks/use-resource"

import { useReportFormat } from "@/hooks/use-report-format"
import { ServiceIssueTable } from "./service-issue-list"

/** 团队表现的筛选范围：publicQueue 限定公共队列，否则 teamId 限定团队，两者都未给出表示全部队列。 */
export type TeamReportFilter = {
  days: number
  channelId: string
  teamId: string
  publicQueue: boolean
}

/** 团队表现问题会话的类型筛选，第一个为默认值。 */
export const teamIssueTypes: ServiceIssueType[] = [
  ServiceIssueType.All,
  ServiceIssueType.Dissatisfied,
  ServiceIssueType.HumanIncorrect,
  ServiceIssueType.HumanPoorAttitude,
]

const pageSize = 50

/** 按客服列出关闭时由其负责的会话数、满意率和问题会话数。 */
export function TeamPerformanceMemberList({ filter }: { filter: TeamReportFilter }) {
  const { t } = useTranslation("agents")
  const { count, rate } = useReportFormat()
  const parameters = { ...filter, pageSize }
  const list = usePagedResource(
    resourceKeys.teamPerformanceMembers(parameters),
    (page) => listTeamPerformanceMembers({ ...parameters, page }),
    {
      select: (data) => ({ items: data.rows, page: data.page }),
      itemKey: (row) => row.identityId,
      keepPreviousData: true,
    },
  )

  return (
    <ResourceListLayout resources={list} errorMessage={t("teamPerformance.loadError")} more={list.more}>
      <ResourceTable
        showHeader
        columns={[
          {
            key: "member",
            header: t("teamPerformance.columns.member"),
            cellClassName: "w-full max-w-0",
            cell: (row) => (
              <ResourceRowIdentity
                avatar={{ imageURL: row.avatarUrl, name: row.displayName, fallback: "person" }}
                name={row.displayName}
              />
            ),
          },
          {
            key: "closed",
            header: t("teamPerformance.columns.closed"),
            className: "w-px whitespace-nowrap text-right tabular-nums",
            cell: (row) => count(row.closed),
          },
          {
            key: "satisfaction",
            header: t("teamPerformance.columns.satisfaction"),
            className: "w-px whitespace-nowrap text-right tabular-nums",
            cell: (row) => rate(row.satisfied, row.satisfactionJudged),
          },
          {
            key: "issues",
            header: t("teamPerformance.columns.issues"),
            className: "w-px whitespace-nowrap text-right tabular-nums",
            cell: (row) => count(row.issues),
          },
        ]}
        rows={list.data?.items ?? []}
        rowKey={(row) => row.identityId}
        empty={t("teamPerformance.noHumanSessions")}
      />
    </ResourceListLayout>
  )
}

/** 按渠道或咨询分类列出已结束会话数、需要真人的数量和占比与真人首响中位数。 */
export function TeamPerformanceBreakdownList({
  dimension,
  filter,
}: {
  dimension: ServiceReportDimension
  filter: TeamReportFilter
}) {
  const { t } = useTranslation("agents")
  const { count, rate, duration } = useReportFormat()
  const parameters = { ...filter, dimension, pageSize }
  const list = usePagedResource(
    resourceKeys.teamPerformanceBreakdowns(parameters),
    (page) => listTeamPerformanceBreakdowns({ ...parameters, page }),
    {
      select: (data) => ({ items: data.rows, page: data.page }),
      itemKey: (row) => row.id || "uncategorized",
      keepPreviousData: true,
    },
  )

  return (
    <ResourceListLayout resources={list} errorMessage={t("teamPerformance.loadError")} more={list.more}>
      <ResourceTable
        showHeader
        columns={[
          {
            key: "name",
            header: t("teamPerformance.columns.name"),
            cellClassName: "max-w-64 truncate",
            cell: (row) => row.name || t("performance.uncategorized"),
          },
          {
            key: "closed",
            header: t("teamPerformance.columns.closed"),
            className: "w-px whitespace-nowrap text-right tabular-nums",
            cell: (row) => count(row.closed),
          },
          {
            key: "humanRequested",
            header: t("teamPerformance.columns.humanRequested"),
            className: "w-px whitespace-nowrap text-right tabular-nums",
            cell: (row) => `${count(row.humanRequested)} · ${rate(row.humanRequested, row.closed)}`,
          },
          {
            key: "firstResponse",
            header: t("teamPerformance.columns.firstResponse"),
            className: "w-px whitespace-nowrap text-right tabular-nums",
            cell: (row) => duration(row.firstResponseMedian ?? null),
          },
        ]}
        rows={list.data?.items ?? []}
        rowKey={(row) => row.id || "uncategorized"}
        empty={t("performance.noSessions")}
      />
    </ResourceListLayout>
  )
}

/** 按关闭时间倒序列出指定类型的真人接待问题会话，点击行在侧栏中查看对话。 */
export function TeamPerformanceIssueList({
  filter,
  issue,
  serviceSessionId,
  onIssueOpen,
}: {
  filter: TeamReportFilter
  issue: ServiceIssueType
  serviceSessionId: string
  onIssueOpen: (serviceSessionId: string) => void
}) {
  const parameters = { ...filter, issue, pageSize }
  return (
    <ServiceIssueTable
      queryKey={resourceKeys.teamPerformanceIssues(parameters)}
      load={(page) => listTeamPerformanceIssues({ ...parameters, page })}
      issueTypes={teamIssueTypes}
      serviceSessionId={serviceSessionId}
      onIssueOpen={onIssueOpen}
    />
  )
}
