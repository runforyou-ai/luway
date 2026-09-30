/** AI 表现报表页：概览、待补知识、问题会话、按渠道与按咨询分类五个与地址同步的页签，共用渠道与 AI 员工筛选；报表页签按结束时间筛选，待补知识按处理状态筛选，问题会话按问题类型筛选，打开的条目编号保存在地址中。 */
import { useTranslation } from "react-i18next"

import {
  ServiceReportDimension,
  getAIPerformanceReport,
  listAgents,
} from "@/api"
import { ListToolbar, ListToolbarFilter } from "@/components/list-toolbar"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

import { mineAgentFilter } from "./agent-navigation"
import { aiIssueTypes, gapStatuses } from "./ai-performance-format"
import {
  AIKnowledgeGapList,
  AIPerformanceBreakdownList,
  AIPerformanceIssueList,
  type ReportFilter,
} from "./ai-performance-lists"
import { AIPerformanceOverview } from "./ai-performance-overview"
import { ReportChannelFilter, ReportPeriodFilter, useReportSearchParams } from "./report-filters"
import { periodOptions } from "./report-format"

/** 页签，第一个为默认值。 */
const reportTabs = ["overview", "knowledgeGaps", "issues", "channels", "categories"] as const

type ReportTab = (typeof reportTabs)[number]

/** 地址参数的默认值，等于默认值时从地址中移除。 */
const parameterDefaults = {
  tab: reportTabs[0],
  days: String(periodOptions[0]),
  channel: "",
  agent: "",
  status: gapStatuses[0],
  gap: "",
  issue: aiIssueTypes[0],
  session: "",
}

/** 打开条目的地址参数。 */
const openItems = ["gap", "session"] as const

/** 显示 AI 客服表现报表，页签和筛选保存在地址中。 */
export function AIPerformancePage() {
  const { t } = useTranslation("agents")
  const [searchParams, setParameters] = useReportSearchParams(parameterDefaults, openItems)
  const days =
    periodOptions.find((option) => String(option) === searchParams.get("days")) ??
    periodOptions[0]
  const channelId = searchParams.get("channel") ?? ""
  const agent = searchParams.get("agent") ?? ""
  const filter: ReportFilter = {
    channelId,
    agentId: agent === mineAgentFilter ? "" : agent,
    mine: agent === mineAgentFilter,
  }
  // 选定渠道时不显示按渠道拆分。
  const tabs = reportTabs.filter((value) => !(channelId && value === "channels"))
  const tab = tabs.find((value) => value === searchParams.get("tab")) ?? tabs[0]
  const status = gapStatuses.find((value) => value === searchParams.get("status")) ?? gapStatuses[0]
  const gapId = searchParams.get("gap") ?? ""
  const issue = aiIssueTypes.find((value) => value === searchParams.get("issue")) ?? aiIssueTypes[0]
  const sessionId = searchParams.get("session") ?? ""

  const agents = useResource(resourceKeys.agents({ pageSize: 100 }), () =>
    listAgents({ pageSize: 100 }),
  )
  const report = useResource(
    resourceKeys.aiPerformanceReport({ days, ...filter }),
    () => getAIPerformanceReport({ days, ...filter }),
    { keepPreviousData: true, enabled: tab === "overview" },
  )

  return (
    <section className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("performance.title")} description={t("performance.description")} />

      <div className="app-page-gutter shrink-0">
        <Tabs value={tab} onValueChange={(value) => setParameters({ tab: value as ReportTab })}>
          <TabsList>
            {tabs.map((value) => (
              <TabsTrigger key={value} value={value}>
                {t(`performance.tabs.${value}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>

      <ListToolbar>
        {/* 待补知识是待办队列，不按结束时间筛选。 */}
        {tab === "knowledgeGaps" ? null : (
          <ReportPeriodFilter value={days} onValueChange={(value) => setParameters({ days: value })} />
        )}
        <ReportChannelFilter value={channelId} onValueChange={(value) => setParameters({ channel: value })} />
        <ListToolbarFilter
          label={t("performance.agent")}
          allLabel={t("performance.allAgents")}
          value={agent}
          options={[
            { value: mineAgentFilter, label: t("performance.mineAgents") },
            ...(agents.data?.agents ?? []).map((item) => ({
              value: item.id,
              label: item.displayName,
            })),
          ]}
          onValueChange={(value) => setParameters({ agent: value })}
        />
        {tab === "knowledgeGaps" ? (
          <ListToolbarFilter
            label={t("performance.gapStatus")}
            value={status}
            options={gapStatuses.map((value) => ({
              value,
              label: t(`performance.gapStatuses.${value}`),
            }))}
            onValueChange={(value) => setParameters({ status: value })}
          />
        ) : null}
        {tab === "issues" ? (
          <ListToolbarFilter
            label={t("performance.issueType")}
            value={issue}
            options={aiIssueTypes.map((value) => ({
              value,
              label: t(`performance.issueTypes.${value}`),
            }))}
            onValueChange={(value) => setParameters({ issue: value })}
          />
        ) : null}
      </ListToolbar>

      {tab === "overview" ? (
        <PageContent>
          <ResourceContent resources={report} errorMessage={t("performance.loadError")}>
            {report.data ? (
              <AIPerformanceOverview
                report={report.data}
                onOpenKnowledgeGaps={() =>
                  setParameters({ tab: "knowledgeGaps", status: gapStatuses[0] })
                }
                onOpenIssues={(value) => setParameters({ tab: "issues", issue: value })}
              />
            ) : null}
          </ResourceContent>
        </PageContent>
      ) : tab === "knowledgeGaps" ? (
        <AIKnowledgeGapList
          filter={filter}
          status={status}
          gapId={gapId}
          onGapChange={(value) => setParameters({ gap: value })}
        />
      ) : tab === "issues" ? (
        <AIPerformanceIssueList
          days={days}
          filter={filter}
          issue={issue}
          serviceSessionId={sessionId}
          onIssueOpen={(value) => setParameters({ session: value })}
        />
      ) : (
        <AIPerformanceBreakdownList
          key={tab}
          dimension={
            tab === "channels"
              ? ServiceReportDimension.ServiceReportDimensionChannel
              : ServiceReportDimension.ServiceReportDimensionCategory
          }
          days={days}
          filter={filter}
        />
      )}
    </section>
  )
}
