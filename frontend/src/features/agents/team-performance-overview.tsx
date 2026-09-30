/** 团队表现报表概览：指标卡、真人承接、满意度与真人质检。 */
import { useTranslation } from "react-i18next"

import { ServiceIssueType, type ServiceIssueTypeId, type TeamPerformanceReport } from "@/api"

import { useReportFormat } from "./report-format"
import { EmptyNote, MeterList, ReportSection, StatTile } from "./report-parts"

/** 显示概览指标，点击不满意与质检行进入对应类型的问题会话。 */
export function TeamPerformanceOverview({
  report,
  onOpenIssues,
}: {
  report: TeamPerformanceReport
  onOpenIssues: (issue: ServiceIssueTypeId) => void
}) {
  const { t } = useTranslation("agents")
  const { count, rate, duration } = useReportFormat()
  const assessed = report.satisfied + report.neutral + report.dissatisfied
  const qualityRows: { issue: ServiceIssueTypeId; value: number; total: number }[] = [
    {
      issue: ServiceIssueType.ServiceIssueTypeHumanIncorrect,
      value: report.humanIncorrect,
      total: report.humanIncorrectReviewed,
    },
    {
      issue: ServiceIssueType.ServiceIssueTypeHumanPoorAttitude,
      value: report.humanPoorAttitude,
      total: report.humanPoorAttitudeReviewed,
    },
  ]
  const reviewedRows = qualityRows.filter((row) => row.total > 0)

  return (
    <div className="space-y-8">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile
          label={t("teamPerformance.firstResponse")}
          value={duration(report.firstResponseMedian ?? null)}
          detail={
            report.firstResponseP90 == null
              ? t("teamPerformance.noFirstResponse")
              : t("teamPerformance.firstResponseDetail", { duration: duration(report.firstResponseP90) })
          }
        />
        <StatTile
          label={t("teamPerformance.humanHandling")}
          value={duration(report.humanHandlingMedian ?? null)}
          detail={t("teamPerformance.handlingDetail", { formatted: count(report.humanHandled) })}
        />
        <StatTile
          label={t("teamPerformance.aiHandling")}
          value={duration(report.aiHandlingMedian ?? null)}
          detail={t("teamPerformance.handlingDetail", { formatted: count(report.aiHandled) })}
        />
        <StatTile
          label={t("performance.satisfactionRate")}
          value={rate(report.satisfied, assessed)}
          detail={t("performance.satisfactionDetail", { formatted: count(assessed) })}
        />
      </div>

      <ReportSection title={t("teamPerformance.takeover")}>
        {report.closed > 0 ? (
          <MeterList
            rows={[
              {
                key: "requested",
                label: t("teamPerformance.humanRequested"),
                value: report.humanRequested,
                total: report.closed,
              },
              {
                key: "responded",
                label: t("teamPerformance.humanResponded"),
                value: report.humanResponded,
                total: report.humanRequested,
              },
            ]}
            format={(value, total) => `${count(value)} · ${rate(value, total)}`}
          />
        ) : (
          <EmptyNote>{t("performance.noSessions")}</EmptyNote>
        )}
      </ReportSection>

      <div className="grid gap-8 md:grid-cols-2">
        <ReportSection title={t("performance.satisfaction")}>
          {report.humanHandled > 0 ? (
            <div className="space-y-3">
              <MeterList
                total={report.humanHandled}
                rows={[
                  { key: "satisfied", label: t("performance.satisfactionLevels.satisfied"), value: report.satisfied },
                  { key: "neutral", label: t("performance.satisfactionLevels.neutral"), value: report.neutral },
                  {
                    key: "dissatisfied",
                    label: t("performance.satisfactionLevels.dissatisfied"),
                    value: report.dissatisfied,
                    onSelect: () => onOpenIssues(ServiceIssueType.ServiceIssueTypeDissatisfied),
                  },
                  {
                    key: "undetermined",
                    label: t("performance.undetermined"),
                    value: report.humanHandled - assessed,
                  },
                ]}
                format={(value) => `${count(value)} · ${rate(value, report.humanHandled)}`}
              />
              {report.rated > 0 ? (
                <p className="text-xs text-muted-foreground">
                  {t("performance.visitorRating", {
                    formatted: count(report.rated),
                    rate: rate(report.ratedResolved, report.rated),
                  })}
                </p>
              ) : null}
            </div>
          ) : (
            <EmptyNote>{t("teamPerformance.noHumanSessions")}</EmptyNote>
          )}
        </ReportSection>

        <ReportSection title={t("teamPerformance.quality")}>
          {reviewedRows.length > 0 ? (
            <MeterList
              rows={reviewedRows.map((row) => ({
                key: row.issue,
                label: t(`performance.issueTypes.${row.issue}`),
                value: row.value,
                total: row.total,
                onSelect: () => onOpenIssues(row.issue),
              }))}
              format={(value, total) => `${count(value)} · ${rate(value, total)}`}
            />
          ) : (
            <EmptyNote>{t("teamPerformance.noReviews")}</EmptyNote>
          )}
        </ReportSection>
      </div>
    </div>
  )
}
