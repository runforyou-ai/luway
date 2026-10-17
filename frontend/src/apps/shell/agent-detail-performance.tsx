/** Shell 组合：AI 员工详情页中的表现相关页签（概览、待补知识、问题会话），装配报表 UI 与待补知识侧栏。 */
import { getAIPerformanceReport } from "@/api"
import { ListToolbar, ListToolbarFilter } from "@/components/list-toolbar"
import { ReportPeriodFilter } from "@/components/metrics"
import { PageContent } from "@/components/page-content"
import { ResourceContent } from "@/components/resource-content"
import type { AgentPerformanceSlotContext } from "@/features/agents/public"
import { AIKnowledgeGapSheet } from "@/features/knowledge-gaps/public"
import {
  AIKnowledgeGapList,
  AIPerformanceIssueList,
  AIPerformanceOverview,
  aiIssueTypes,
  gapStatuses,
} from "@/features/reports/public"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { useTranslation } from "react-i18next"

/** 按当前页签渲染 AI 员工表现内容。 */
export function AgentDetailPerformance({
  agent,
  tab,
  days,
  status,
  gapId,
  issue,
  sessionId,
  setParameters,
}: AgentPerformanceSlotContext) {
  const { t } = useTranslation("agents")
  const filter = { channelId: "", agentId: agent.id, mine: false }
  const report = useResource(
    resourceKeys.aiPerformanceReport({ days, ...filter }),
    () => getAIPerformanceReport({ days, ...filter }),
    { keepPreviousData: true, enabled: tab === "overview" },
  )

  if (tab === "overview") {
    return (
      <>
        <ListToolbar>
          <ReportPeriodFilter value={days} onValueChange={(value) => setParameters({ days: value })} />
        </ListToolbar>
        <PageContent>
          <ResourceContent resources={report} errorMessage={t("performance.loadError")}>
            {report.data ? (
              <AIPerformanceOverview
                report={report.data}
                onOpenKnowledgeGaps={() => setParameters({ tab: "knowledgeGaps", status: gapStatuses[0] })}
                onOpenIssues={(value) => setParameters({ tab: "issues", issue: value })}
              />
            ) : null}
          </ResourceContent>
        </PageContent>
      </>
    )
  }

  if (tab === "knowledgeGaps") {
    return (
      <>
        <ListToolbar>
          <ListToolbarFilter
            label={t("performance.gapStatus")}
            value={status}
            options={gapStatuses.map((value) => ({
              value,
              label: t(`performance.gapStatuses.${value}`),
            }))}
            onValueChange={(value) => setParameters({ status: value })}
          />
        </ListToolbar>
        <AIKnowledgeGapList
          filter={filter}
          status={status}
          gapId={gapId}
          onGapChange={(value) => setParameters({ gap: value })}
          KnowledgeGapSheet={AIKnowledgeGapSheet}
        />
      </>
    )
  }

  if (tab === "issues") {
    return (
      <>
        <ListToolbar>
          <ReportPeriodFilter value={days} onValueChange={(value) => setParameters({ days: value })} />
          <ListToolbarFilter
            label={t("performance.issueType")}
            value={issue}
            options={aiIssueTypes.map((value) => ({
              value,
              label: t(`performance.issueTypes.${value}`),
            }))}
            onValueChange={(value) => setParameters({ issue: value })}
          />
        </ListToolbar>
        <AIPerformanceIssueList
          days={days}
          filter={filter}
          issue={issue}
          serviceSessionId={sessionId}
          onIssueOpen={(value) => setParameters({ session: value })}
        />
      </>
    )
  }

  return null
}
