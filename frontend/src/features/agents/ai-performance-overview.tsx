/** AI 表现报表概览：指标卡、解决情况、满意度、AI 质检、结束方式与转人工原因分布。 */
import { useTranslation } from "react-i18next"

import {
  AgentHandoffReason,
  ServiceIssueType,
  type ServiceIssueTypeId,
  type AIPerformanceReportData,
} from "@/api"
import { handoffReasonKey } from "@/lib/handoff-reason-labels"

import { useReportFormat } from "./report-format"
import { EmptyNote, MeterList, ReportSection, StatTile } from "@/components/report-parts"

/** 模型调用 handoff_to_human 时可给出的业务原因，其余为系统原因。 */
const businessReasons: readonly AgentHandoffReason[] = [
  AgentHandoffReason.AgentHandoffReasonKnowledgeGap,
  AgentHandoffReason.AgentHandoffReasonCustomerRequested,
  AgentHandoffReason.AgentHandoffReasonNeedsHumanJudgment,
  AgentHandoffReason.AgentHandoffReasonComplaint,
]

/** 显示概览指标，点击待补知识指标卡进入待补知识页签，点击不满意与质检行进入对应类型的问题会话。 */
export function AIPerformanceOverview({
  report,
  onOpenKnowledgeGaps,
  onOpenIssues,
}: {
  report: AIPerformanceReportData
  onOpenKnowledgeGaps: () => void
  onOpenIssues: (issue: ServiceIssueTypeId) => void
}) {
  const { t } = useTranslation(["agents", "inbox"])
  const { count, rate } = useReportFormat()
  const { summary, handoffReasons } = report
  const handoffTotal = handoffReasons.reduce((sum, item) => sum + item.count, 0)
  const assessed = summary.satisfied + summary.neutral + summary.dissatisfied
  const qualityChecks: { issue: ServiceIssueTypeId; value: number; total: number }[] = [
    {
      issue: ServiceIssueType.ServiceIssueTypeAIIncorrect,
      value: summary.aiIncorrect,
      total: summary.aiIncorrectReviewed,
    },
    {
      issue: ServiceIssueType.ServiceIssueTypeAIMissedHandoff,
      value: summary.aiMissedHandoff,
      total: summary.aiMissedHandoffReviewed,
    },
    {
      issue: ServiceIssueType.ServiceIssueTypeAIPoorAttitude,
      value: summary.aiPoorAttitude,
      total: summary.aiPoorAttitudeReviewed,
    },
  ]
  const qualityRows = qualityChecks.filter((row) => row.total > 0)

  return (
    <div className="space-y-8">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile
          label={t("performance.aiResolvedRate")}
          value={rate(summary.aiResolved, summary.closed)}
          detail={t("performance.aiResolvedDetail", {
            resolved: count(summary.aiResolved),
            closed: count(summary.closed),
          })}
        />
        <StatTile
          label={t("performance.satisfactionRate")}
          value={rate(summary.satisfied, assessed)}
          detail={t("performance.satisfactionDetail", { formatted: count(assessed) })}
        />
        <StatTile
          label={t("performance.handoffRate")}
          value={rate(summary.handedOff, summary.closed)}
          detail={t("performance.handoffRateDetail", { formatted: count(summary.handedOff) })}
        />
        <StatTile
          label={t("performance.gaps")}
          value={count(report.knowledgeGapTotal)}
          detail={t("performance.gapsDetail")}
          onClick={onOpenKnowledgeGaps}
        />
      </div>

      <ReportSection title={t("performance.resolution")}>
        {summary.closed > 0 ? (
          <div className="grid gap-x-8 gap-y-5 md:grid-cols-2">
            {[
              {
                key: "ai",
                title: t("performance.aiOnly"),
                total: summary.aiOnly,
                resolved: summary.aiResolved,
                unresolved: summary.aiUnresolved,
              },
              {
                key: "human",
                title: t("performance.humanInvolved"),
                total: summary.closed - summary.aiOnly,
                resolved: summary.resolved - summary.aiResolved,
                unresolved: summary.unresolved - summary.aiUnresolved,
              },
            ].map((group) => {
              if (group.total === 0) return null
              return (
                <div key={group.key} className="space-y-2">
                  <p className="text-xs text-muted-foreground">
                    {t("performance.resolutionGroup", { name: group.title, formatted: count(group.total) })}
                  </p>
                  <MeterList
                    total={group.total}
                    rows={[
                      { key: "resolved", label: t("performance.resolved"), value: group.resolved },
                      { key: "unresolved", label: t("performance.unresolved"), value: group.unresolved },
                      {
                        key: "undetermined",
                        label: t("performance.undetermined"),
                        value: group.total - group.resolved - group.unresolved,
                      },
                    ]}
                    format={(value) => `${count(value)} · ${rate(value, group.total)}`}
                  />
                </div>
              )
            })}
          </div>
        ) : (
          <EmptyNote>{t("performance.noSessions")}</EmptyNote>
        )}
      </ReportSection>

      <div className="grid gap-8 md:grid-cols-2">
        <ReportSection title={t("performance.satisfaction")}>
          {summary.closed > 0 ? (
            <div className="space-y-3">
              <MeterList
                total={summary.closed}
                rows={[
                  { key: "satisfied", label: t("performance.satisfactionLevels.satisfied"), value: summary.satisfied },
                  { key: "neutral", label: t("performance.satisfactionLevels.neutral"), value: summary.neutral },
                  {
                    key: "dissatisfied",
                    label: t("performance.satisfactionLevels.dissatisfied"),
                    value: summary.dissatisfied,
                    onSelect: () => onOpenIssues(ServiceIssueType.ServiceIssueTypeDissatisfied),
                  },
                  { key: "undetermined", label: t("performance.undetermined"), value: summary.closed - assessed },
                ]}
                format={(value) => `${count(value)} · ${rate(value, summary.closed)}`}
              />
              {summary.rated > 0 ? (
                <p className="text-xs text-muted-foreground">
                  {t("performance.visitorRating", {
                    formatted: count(summary.rated),
                    rate: rate(summary.ratedResolved, summary.rated),
                  })}
                </p>
              ) : null}
            </div>
          ) : (
            <EmptyNote>{t("performance.noSessions")}</EmptyNote>
          )}
        </ReportSection>

        <ReportSection title={t("performance.quality")}>
          {qualityRows.length > 0 ? (
            <MeterList
              rows={qualityRows.map((row) => ({
                key: row.issue,
                label: t(`performance.issueTypes.${row.issue}`),
                value: row.value,
                total: row.total,
                onSelect: () => onOpenIssues(row.issue),
              }))}
              format={(value, total) => `${count(value)} · ${rate(value, total)}`}
            />
          ) : (
            <EmptyNote>{t("performance.noReviews")}</EmptyNote>
          )}
        </ReportSection>
      </div>

      <div className="grid gap-8 md:grid-cols-2">
        <ReportSection title={t("performance.closeReasons")}>
          {summary.closed > 0 ? (
            <MeterList
              total={summary.closed}
              rows={[
                { key: "ai", label: t("performance.closeAIResolved"), value: summary.closeAiResolved },
                { key: "unresponsive", label: t("performance.closeCustomerUnresponsive"), value: summary.customerUnresponsive },
                { key: "manual", label: t("performance.closeManual"), value: summary.manual },
              ]}
              format={(value) => `${count(value)} · ${rate(value, summary.closed)}`}
            />
          ) : (
            <EmptyNote>{t("performance.noSessions")}</EmptyNote>
          )}
        </ReportSection>

        <ReportSection title={t("performance.handoffReasons")}>
          {handoffTotal > 0 ? (
            <div className="space-y-5">
              {[
                { key: "business", title: t("performance.businessReasons"), business: true },
                { key: "system", title: t("performance.systemReasons"), business: false },
              ].map((group) => {
                const rows = handoffReasons.filter(
                  (item) => businessReasons.includes(item.reason) === group.business,
                )
                if (rows.length === 0) return null
                return (
                  <div key={group.key} className="space-y-2">
                    <p className="text-xs text-muted-foreground">{group.title}</p>
                    <MeterList
                      total={handoffTotal}
                      rows={rows.map((item) => ({
                        key: item.reason,
                        label: t(`inbox:${handoffReasonKey(item.reason)}`),
                        value: item.count,
                      }))}
                      format={(value) => `${count(value)} · ${rate(value, handoffTotal)}`}
                    />
                  </div>
                )
              })}
            </div>
          ) : (
            <EmptyNote>{t("performance.noHandoffs")}</EmptyNote>
          )}
        </ReportSection>
      </div>
    </div>
  )
}
