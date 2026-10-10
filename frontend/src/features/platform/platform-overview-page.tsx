/** 平台设置的概览页：平台规模页签展示账号、工作区与活跃规模和近 30 天每日活跃趋势，业务使用页签展示各工作区的服务指标，运行状态与近期错误页签展示服务端与后台任务的运行情况，日志页签展示各服务器的服务端日志。 */
import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import { getPlatformOverview } from "@/api"
import { PageContent } from "@/components/page-content"
import { DailyBarChart, ReportSection, StatTile } from "@/components/report-parts"
import { ResourceContent } from "@/components/resource-content"
import { PlatformTabsPage } from "@/features/platform/platform-tabs"
import { PlatformErrorListTab } from "@/features/platform/platform-error-list-tab"
import { PlatformLogListTab } from "@/features/platform/platform-log-list-tab"
import { PlatformRuntimeTab } from "@/features/platform/platform-runtime-tab"
import { PlatformUsageTab } from "@/features/platform/platform-usage-tab"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 按页签展示平台规模、业务使用、运行状态、近期错误或日志。 */
export function PlatformOverviewPage() {
  const { t } = useTranslation("platform")
  return (
    <PlatformTabsPage
      title={t("overview.title")}
      description={t("overview.description")}
      tabs={[
        { value: "scale", label: t("overview.tabs.scale") },
        { value: "usage", label: t("overview.tabs.usage") },
        { value: "runtime", label: t("overview.tabs.runtime") },
        { value: "errors", label: t("overview.tabs.errors") },
        { value: "logs", label: t("overview.tabs.logs") },
      ]}
    >
      {(tab) =>
        tab === "scale" ? (
          <PlatformScaleTab />
        ) : tab === "usage" ? (
          <PlatformUsageTab />
        ) : tab === "runtime" ? (
          <PlatformRuntimeTab />
        ) : tab === "errors" ? (
          <PlatformErrorListTab />
        ) : (
          <PlatformLogListTab />
        )
      }
    </PlatformTabsPage>
  )
}

/** 展示平台规模与近 30 天活跃趋势，按新时区重建期间每隔几秒刷新。 */
function PlatformScaleTab() {
  const { t, i18n } = useTranslation("platform")
  const overview = useResource(resourceKeys.platformOverview(), (signal) => getPlatformOverview(signal), {
    staleTime: 0,
    refetchInterval: (current) => (current?.statsRebuilding ? 3000 : false),
  })
  const data = overview.data
  const count = useMemo(() => new Intl.NumberFormat(i18n.resolvedLanguage), [i18n.resolvedLanguage])
  const dayLabel = useMemo(
    () => new Intl.DateTimeFormat(i18n.resolvedLanguage, { month: "numeric", day: "numeric", timeZone: "UTC" }),
    [i18n.resolvedLanguage],
  )

  return (
    <PageContent>
      <ResourceContent resources={overview} errorMessage={t("overview.loadError")}>
        {data ? (
          <div className="space-y-9">
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              <StatTile
                label={t("overview.accountCount")}
                value={count.format(data.accountCount)}
                detail={t("overview.newInLast30Days", { formatted: count.format(data.last30Days.newAccounts) })}
              />
              <StatTile
                label={t("overview.workspaceCount")}
                value={count.format(data.workspaceCount)}
                detail={t("overview.memberTotal", { formatted: count.format(data.memberCount) })}
              />
              <StatTile
                label={t("overview.activeAccounts")}
                value={count.format(data.last7Days.activeAccounts)}
                detail={t("overview.activeIn30Days", { formatted: count.format(data.last30Days.activeAccounts) })}
              />
              <StatTile
                label={t("overview.activeWorkspaces")}
                value={count.format(data.last7Days.activeWorkspaces)}
                detail={t("overview.activeIn30Days", { formatted: count.format(data.last30Days.activeWorkspaces) })}
              />
            </div>

            <ReportSection title={t("overview.trendTitle")}>
              {data.statsRebuilding ? (
                <p className="text-sm text-muted-foreground">{t("overview.statsRebuilding")}</p>
              ) : data.trend.some((day) => day.activeAccounts > 0) ? (
                <DailyBarChart
                  label={t("overview.trendTitle")}
                  days={data.trend.map((day) => {
                    const label = dayLabel.format(new Date(`${day.date}T00:00:00Z`))
                    return {
                      key: day.date,
                      label,
                      value: day.activeAccounts,
                      detail: t("overview.trendDetail", {
                        date: label,
                        accounts: count.format(day.activeAccounts),
                        workspaces: count.format(day.activeWorkspaces),
                        newAccounts: count.format(day.newAccounts),
                      }),
                    }
                  })}
                />
              ) : (
                <p className="text-sm text-muted-foreground">{t("overview.trendEmpty")}</p>
              )}
            </ReportSection>
          </div>
        ) : null}
      </ResourceContent>
    </PageContent>
  )
}
