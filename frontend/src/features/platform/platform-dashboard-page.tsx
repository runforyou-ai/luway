/** 平台后台仪表板：按需关注、业务使用与平台规模分区汇集高价值指标；托管构建可追加商业摘要。 */
import { useMemo, type ReactNode } from "react"
import {
  CircleAlertIcon,
  KeyRoundIcon,
  ServerCogIcon,
  type LucideIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import {
  getLicense,
  getPlatformOverview,
  getPlatformRuntimeStatus,
  getPlatformUsage,
  LicenseStatus,
  listPlatformFailedTasks,
  type License,
} from "@/api"
import { appExtensions } from "@/app-extensions"
import { DailyBarChart, ReportSection, StatTile } from "@/components/metrics"
import { PageContent } from "@/components/page-content"
import { ResourceContent, type ResourceState } from "@/components/resource-content"
import { AdminPage } from "@/features/admin/admin-page"
import {
  adminAccountsPath,
  adminLicensePath,
  adminOverviewSectionPath,
  adminWorkspacesPath,
} from "@/features/admin/admin-paths"
import { licenseRemainingDays, renewalReminderDays } from "@/features/platform/license-reminder"
import { runtimeRefreshInterval } from "@/features/platform/platform-runtime-tab"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useReportFormat } from "@/hooks/use-report-format"
import { useResource } from "@/hooks/use-resource"
import { cn } from "@/lib/utils"

/** 仪表板业务使用默认看近 7 天。 */
const dashboardUsageDays = 7

/** 平台后台首页仪表板。 */
export function PlatformDashboardPage() {
  const { t } = useTranslation("platform")
  const extensionSections = appExtensions().useAdminDashboardSections()
  return (
    <AdminPage title={t("dashboard.title")} description={t("dashboard.description")}>
      <PageContent>
        <div className="space-y-9">
          <DashboardAttentionSection />
          <DashboardBusinessSection />
          {extensionSections}
          <DashboardScaleSection />
        </div>
      </PageContent>
    </AdminPage>
  )
}

/** 需关注：授权、任务积压与近期错误；异常用警示色。 */
function DashboardAttentionSection() {
  const { t } = useTranslation("platform")
  return (
    <ReportSection title={t("dashboard.attentionTitle")}>
      <div className="grid gap-3 md:grid-cols-3">
        <DashboardLicenseCard />
        <DashboardRuntimeCard />
        <DashboardErrorsCard />
      </div>
    </ReportSection>
  )
}

/** 近 7 天业务使用：服务负载、AI 效果、待补知识与模型失败率。 */
function DashboardBusinessSection() {
  const { t } = useTranslation("platform")
  const navigate = useNavigate()
  const { count, rate } = useReportFormat()
  const usage = useResource(
    resourceKeys.platformUsage({ days: dashboardUsageDays }),
    (signal) => getPlatformUsage({ days: dashboardUsageDays }, signal),
    { staleTime: 0 },
  )
  const total = usage.data
  const usagePath = `${adminOverviewSectionPath("usage")}?days=${dashboardUsageDays}`

  return (
    <ReportSection title={t("dashboard.businessTitle")}>
      <ResourceContent resources={usage} errorMessage={t("usage.loadError")}>
        {total ? (
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <StatTile
              label={t("usage.serviceSessions")}
              value={count(total.serviceSessions)}
              detail={t("usage.conversationsDetail", { formatted: count(total.conversations) })}
              onClick={() => navigate(usagePath)}
            />
            <StatTile
              label={t("usage.aiResolvedRate")}
              value={rate(total.aiResolved, total.aiClosed)}
              detail={t("usage.aiResolvedDetail", {
                resolved: count(total.aiResolved),
                closed: count(total.aiClosed),
                rate: rate(total.handedOff, total.aiClosed),
              })}
              onClick={() => navigate(usagePath)}
            />
            <StatTile
              label={t("usage.knowledgeGaps")}
              value={count(total.knowledgeGaps)}
              detail={t("usage.knowledgeGapsDetail")}
              warning={total.knowledgeGaps > 0}
              onClick={() => navigate(usagePath)}
            />
            <StatTile
              label={t("dashboard.modelFailureRate")}
              value={rate(total.modelCallsFailed, total.modelCallsConcluded)}
              detail={t("usage.modelCallsDetail", {
                rate: rate(total.modelCallsFailed, total.modelCallsConcluded),
                failed: count(total.modelCallsFailed),
                concluded: count(total.modelCallsConcluded),
              })}
              warning={total.modelCallsConcluded > 0 && total.modelCallsFailed / total.modelCallsConcluded >= 0.05}
              onClick={() => navigate(usagePath)}
            />
          </div>
        ) : null}
      </ResourceContent>
    </ReportSection>
  )
}

/** 平台规模与近 30 天活跃趋势。 */
function DashboardScaleSection() {
  const { t, i18n } = useTranslation("platform")
  const navigate = useNavigate()
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
    <ReportSection title={t("dashboard.scaleTitle")}>
      <ResourceContent resources={overview} errorMessage={t("overview.loadError")}>
        {data ? (
          <div className="space-y-6">
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              <StatTile
                label={t("overview.accountCount")}
                value={count.format(data.accountCount)}
                detail={t("overview.newInLast30Days", { formatted: count.format(data.last30Days.newAccounts) })}
                onClick={() => navigate(adminAccountsPath)}
              />
              <StatTile
                label={t("overview.workspaceCount")}
                value={count.format(data.workspaceCount)}
                detail={t("dashboard.newWorkspacesIn30Days", {
                  formatted: count.format(data.last30Days.newWorkspaces),
                })}
                onClick={() => navigate(adminWorkspacesPath)}
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

            <div className="space-y-3">
              <h4 className="text-sm font-medium">{t("overview.trendTitle")}</h4>
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
            </div>
          </div>
        ) : null}
      </ResourceContent>
    </ReportSection>
  )
}

/** 授权状态摘要卡。 */
function DashboardLicenseCard() {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const license = useResource(resourceKeys.license(), (signal) => getLicense(signal), {
    staleTime: 0,
    recoverSession: false,
  })

  return (
    <DashboardSummaryCard
      icon={KeyRoundIcon}
      title={t("license.title")}
      to={adminLicensePath}
      resources={license}
      errorMessage={t("license.loadError")}
    >
      {license.data ? <LicenseSummary license={license.data} formatDateTime={formatDateTime} /> : null}
    </DashboardSummaryCard>
  )
}

/** 授权摘要正文。 */
function LicenseSummary({
  license,
  formatDateTime,
}: {
  license: License
  formatDateTime: (value: string) => string
}) {
  const { t } = useTranslation("platform")
  const labels = {
    [LicenseStatus.None]: t("license.statuses.none"),
    [LicenseStatus.Active]: t("license.statuses.active"),
    [LicenseStatus.Expired]: t("license.statuses.expired"),
  }
  const remaining = license.status === LicenseStatus.Active ? licenseRemainingDays(license) : null
  const warning =
    license.status === LicenseStatus.Expired ||
    license.status === LicenseStatus.None ||
    (remaining != null && remaining <= renewalReminderDays)
  const detail =
    license.status === LicenseStatus.None
      ? t("license.statusHelp.none")
      : license.status === LicenseStatus.Expired
        ? t("license.statusHelp.expired")
        : remaining != null && remaining <= renewalReminderDays
          ? t("license.statusHelp.expiring", { count: remaining })
          : license.expiresAt
            ? t("dashboard.licenseExpires", { date: formatDateTime(license.expiresAt) })
            : t("license.statuses.active")

  return (
    <>
      <p className={cn("text-2xl font-semibold tabular-nums", warning && "text-warning")}>{labels[license.status]}</p>
      <p className={cn("mt-1 text-xs text-pretty", warning ? "text-warning" : "text-muted-foreground")}>{detail}</p>
    </>
  )
}

/** 运行状态摘要卡：突出等待、延迟与失败。 */
function DashboardRuntimeCard() {
  const { t, i18n } = useTranslation("platform")
  const count = useMemo(() => new Intl.NumberFormat(i18n.resolvedLanguage), [i18n.resolvedLanguage])
  const status = useResource(resourceKeys.platformRuntime(), (signal) => getPlatformRuntimeStatus(signal), {
    staleTime: 0,
    refetchInterval: runtimeRefreshInterval,
  })
  const servers = status.data?.servers ?? []
  const online = servers.filter((server) => server.online).length
  const waiting = status.data?.queues.reduce((sum, queue) => sum + queue.waiting, 0) ?? 0
  const failed = status.data?.queues.reduce((sum, queue) => sum + queue.failed, 0) ?? 0
  const delayed = status.data?.delayedTasks ?? 0
  const lost = servers.length - online
  const busIssues = servers.filter((server) => server.online && (!server.busConnected || server.busFailures > 0)).length
  const warning = lost > 0 || failed > 0 || waiting > 0 || delayed > 0 || busIssues > 0

  return (
    <DashboardSummaryCard
      icon={ServerCogIcon}
      title={t("overview.tabs.runtime")}
      to={adminOverviewSectionPath("runtime")}
      resources={status}
      errorMessage={t("runtime.loadError")}
    >
      {status.data ? (
        <>
          <p className={cn("text-2xl font-semibold tabular-nums", warning && "text-warning")}>
            {t("dashboard.taskBacklog", {
              waiting: count.format(waiting),
              delayed: count.format(delayed),
            })}
          </p>
          <p className={cn("mt-1 text-xs text-pretty", warning ? "text-warning" : "text-muted-foreground")}>
            {t("dashboard.runtimeDetail", {
              online: count.format(online),
              total: count.format(servers.length),
              failed: count.format(failed),
            })}
          </p>
        </>
      ) : null}
    </DashboardSummaryCard>
  )
}

/** 近期错误摘要卡：只展示条数，明细进近期错误页。 */
function DashboardErrorsCard() {
  const { t, i18n } = useTranslation("platform")
  const count = useMemo(() => new Intl.NumberFormat(i18n.resolvedLanguage), [i18n.resolvedLanguage])
  const failed = useResource(
    resourceKeys.platformFailedTasks({ page: 1, pageSize: 1 }),
    (signal) => listPlatformFailedTasks({ page: 1, pageSize: 1 }, signal),
    { staleTime: 0, refetchInterval: runtimeRefreshInterval },
  )
  const total = failed.data?.page.total ?? 0
  const warning = total > 0

  return (
    <DashboardSummaryCard
      icon={CircleAlertIcon}
      title={t("overview.tabs.errors")}
      to={adminOverviewSectionPath("errors")}
      resources={failed}
      errorMessage={t("runtime.loadError")}
    >
      {failed.data ? (
        <>
          <p className={cn("text-2xl font-semibold tabular-nums", warning && "text-warning")}>
            {t("dashboard.errorCount", { formatted: count.format(total) })}
          </p>
          {!warning ? (
            <p className="mt-1 text-xs text-pretty text-muted-foreground">{t("dashboard.errorsEmpty")}</p>
          ) : null}
        </>
      ) : null}
    </DashboardSummaryCard>
  )
}

/** 仪表板摘要卡外壳：整卡可点击进入明细页。 */
function DashboardSummaryCard({
  icon: Icon,
  title,
  to,
  resources,
  errorMessage,
  children,
}: {
  icon: LucideIcon
  title: string
  to: string
  resources: ResourceState | readonly ResourceState[]
  errorMessage: string
  children: ReactNode
}) {
  const navigate = useNavigate()
  const open = () => navigate(to)
  return (
    <div
      role="link"
      tabIndex={0}
      className="cursor-pointer rounded-lg border border-border/55 px-4 py-3 text-left transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("button, a")) return
        open()
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault()
          open()
        }
      }}
    >
      <div className="mb-2 flex items-center gap-2">
        <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <h3 className="min-w-0 flex-1 truncate text-sm font-medium">{title}</h3>
      </div>
      <ResourceContent resources={resources} errorMessage={errorMessage}>
        {children}
      </ResourceContent>
    </div>
  )
}
