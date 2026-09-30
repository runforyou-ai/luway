/** AI 表现报表的滚动加载列表：按渠道或咨询分类拆分、待补知识清单与 AI 表现问题会话。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  ServiceReportDimension,
  dismissKnowledgeGap,
  isApiError,
  KnowledgeGapSource,
  KnowledgeGapStatus,
  listAIPerformanceBreakdowns,
  listAIPerformanceIssues,
  listKnowledgeGaps,
  type ServiceIssueTypeId,
  type KnowledgeGapStatusId,
} from "@/api"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceTable } from "@/components/resource-table"
import { useDateTime } from "@/hooks/use-date-time"
import { resourceKeys } from "@/hooks/resource-keys"
import { usePagedResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

import { AIKnowledgeGapSheet } from "./ai-knowledge-gap-sheet"
import { aiIssueTypes } from "./ai-performance-format"
import { useReportFormat } from "./report-format"
import { ServiceIssueTable } from "./service-issue-list"

/** 报表与待补知识共用的筛选范围：agentId 限定单个 AI 员工，mine 限定为本人负责的 AI 员工。 */
export type ReportFilter = {
  channelId: string
  agentId: string
  mine: boolean
}

const pageSize = 50

/** 按渠道或咨询分类列出已结束会话数，以及已解决与 AI 独立解决的数量和占比。 */
export function AIPerformanceBreakdownList({
  dimension,
  days,
  filter,
}: {
  dimension: ServiceReportDimension
  days: number
  filter: ReportFilter
}) {
  const { t } = useTranslation("agents")
  const { count, rate } = useReportFormat()
  const parameters = { days, ...filter, dimension, pageSize }
  const list = usePagedResource(
    resourceKeys.aiPerformanceBreakdowns(parameters),
    (page) => listAIPerformanceBreakdowns({ ...parameters, page }),
    {
      select: (data) => ({ items: data.rows, page: data.page }),
      itemKey: (row) => row.id || "uncategorized",
      keepPreviousData: true,
    },
  )

  return (
    <ResourceListLayout
      resources={list}
      errorMessage={t("performance.loadError")}
      more={list.more}
    >
      <ResourceTable
        showHeader
        columns={[
          {
            key: "name",
            header: t("performance.columns.name"),
            cellClassName: "max-w-64 truncate",
            cell: (row) => row.name || t("performance.uncategorized"),
          },
          {
            key: "closed",
            header: t("performance.columns.closed"),
            className: "w-28 text-right tabular-nums",
            cell: (row) => count(row.closed),
          },
          {
            key: "resolved",
            header: t("performance.columns.resolved"),
            className: "w-36 text-right tabular-nums",
            cell: (row) => `${count(row.resolved)} · ${rate(row.resolved, row.closed)}`,
          },
          {
            key: "aiResolved",
            header: t("performance.columns.aiResolved"),
            className: "w-36 text-right tabular-nums",
            cell: (row) => `${count(row.aiResolved)} · ${rate(row.aiResolved, row.closed)}`,
          },
        ]}
        rows={list.data?.items ?? []}
        rowKey={(row) => row.id || "uncategorized"}
        empty={t("performance.noSessions")}
      />
    </ResourceListLayout>
  )
}

/** 列出指定处理状态的待补知识，点击行在侧栏中处理，处理完一条自动打开清单中的下一条。 */
export function AIKnowledgeGapList({
  filter,
  status,
  gapId,
  onGapChange,
}: {
  filter: ReportFilter
  status: KnowledgeGapStatusId
  gapId: string
  onGapChange: (gapId: string) => void
}) {
  const { t } = useTranslation("agents")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const { formatDateTime } = useDateTime()
  const parameters = { ...filter, status, pageSize }
  const list = usePagedResource(
    resourceKeys.knowledgeGaps(parameters),
    (page) => listKnowledgeGaps({ ...parameters, page }),
    {
      select: (data) => ({ items: data.gaps, page: data.page }),
      itemKey: (gap) => gap.id,
      keepPreviousData: true,
    },
  )
  const rows = list.data?.items ?? []
  const pending = status === KnowledgeGapStatus.KnowledgeGapStatusPending

  /** 忽略清单中的一条待补知识。 */
  async function dismiss(id: string) {
    try {
      await dismissKnowledgeGap(id)
      await Promise.all([
        invalidate(resourceKeys.knowledgeGaps()),
        invalidate(resourceKeys.knowledgeGap(id)),
        invalidate(resourceKeys.aiPerformanceReport()),
      ])
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("忽略待补知识失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("performance.gapSheet.dismissError"))
    }
  }

  return (
    <>
      <ResourceListLayout
        resources={list}
        errorMessage={t("performance.loadError")}
        more={list.more}
      >
        <ResourceTable
          columns={[
            {
              key: "question",
              header: t("performance.gapQuestion"),
              cellClassName: "w-full max-w-0",
              cell: (gap) => (
                <div className="min-w-0">
                  <p className="truncate">{gap.question || t("performance.noQuestion")}</p>
                  <p className="truncate text-xs text-muted-foreground">
                    {[
                      gap.categoryName || t("performance.uncategorized"),
                      t(`performance.gapSources.${gap.source}`),
                      pending && gap.hasDraft ? t("performance.hasDraft") : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                </div>
              ),
            },
            {
              key: "time",
              header: t("performance.gapTime"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (gap) => t(`performance.gapTimes.${gap.source}`, { time: formatDateTime(gap.occurredAt) }),
            },
          ]}
          rows={rows}
          rowKey={(gap) => gap.id}
          empty={t(`performance.noGaps.${status}`)}
          onRowActivate={(gap) => onGapChange(gap.id)}
          rowActions={(gap) => [
            {
              key: "conversation",
              label: t("performance.viewConversation"),
              onSelect: () => {
                // 在收件箱打开该会话；客户提问已确定时定位到提问，复核来源的提问由起草确定。
                const params = new URLSearchParams({ conversation: gap.conversationId })
                const review =
                  gap.source === KnowledgeGapSource.KnowledgeGapSourceRatedUnresolved ||
                  gap.source === KnowledgeGapSource.KnowledgeGapSourcePossiblyWrong
                if (gap.questionMessageId && (!review || gap.hasDraft)) params.set("message", gap.questionMessageId)
                navigate(`/inbox?${params.toString()}`)
              },
            },
            // 只有待处理的条目可以忽略。
            ...(gap.status === KnowledgeGapStatus.KnowledgeGapStatusPending
              ? [
                  {
                    key: "dismiss",
                    label: t("performance.dismissGap"),
                    separatorBefore: true,
                    onSelect: () => void dismiss(gap.id),
                  },
                ]
              : []),
          ]}
        />
      </ResourceListLayout>
      <AIKnowledgeGapSheet
        gapId={gapId}
        onClose={() => onGapChange("")}
        onHandled={() => {
          // 待处理清单打开当前条目之后的下一条，其余清单处理后关闭侧栏。
          const index = rows.findIndex((gap) => gap.id === gapId)
          onGapChange(pending && index >= 0 ? (rows[index + 1]?.id ?? "") : "")
        }}
      />
    </>
  )
}

/** 按关闭时间倒序列出指定类型的 AI 表现问题会话，点击行在侧栏中查看对话。 */
export function AIPerformanceIssueList({
  days,
  filter,
  issue,
  serviceSessionId,
  onIssueOpen,
}: {
  days: number
  filter: ReportFilter
  issue: ServiceIssueTypeId
  serviceSessionId: string
  onIssueOpen: (serviceSessionId: string) => void
}) {
  const parameters = { days, ...filter, issue, pageSize }
  return (
    <ServiceIssueTable
      queryKey={resourceKeys.aiPerformanceIssues(parameters)}
      load={(page) => listAIPerformanceIssues({ ...parameters, page })}
      issueTypes={aiIssueTypes}
      serviceSessionId={serviceSessionId}
      onIssueOpen={onIssueOpen}
    />
  )
}
