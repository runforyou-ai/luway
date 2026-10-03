/** 平台设置的概览页：账号、工作区与活跃规模，近 30 天每日活跃趋势，平台时区，授权状态，服务器标识、运行指标与错误上报开关、安装时间和工作区上限。 */
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  LicenseStatus,
  getPlatformOverview,
  getPlatformSettings,
  updatePlatformTimeZone,
  updatePlatformTelemetry,
  type License,
  type PlatformSettings,
} from "@/api"
import { SwitchField } from "@/components/form/switch-field"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { DailyBarChart, ReportSection, StatTile } from "@/components/report-parts"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { useDateTime } from "@/hooks/use-date-time"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { useLicenseStatusHelp, useRefreshAtLicenseExpiry } from "@/features/settings/platform/platform-license-page"
import { supportedTimeZones } from "@/lib/time-zones"
import { zodResolver } from "@/lib/zod-resolver"

/** 平台时区表单校验。 */
const timeZoneSchema = z.object({ timeZone: z.string().min(1) })

/** 平台时区表单值。 */
type TimeZoneFormValues = z.infer<typeof timeZoneSchema>

/** 上报开关表单校验。 */
const telemetrySchema = z.object({ telemetryEnabled: z.boolean() })

/** 上报开关表单值。 */
type TelemetryFormValues = z.infer<typeof telemetrySchema>

/** 展示平台规模、活跃趋势和服务器信息，平台时区与上报开关修改后立即保存，服务器标识可一键复制。 */
export function PlatformOverviewPage() {
  const { t, i18n } = useTranslation(["platform", "common"])
  const { formatDateTime } = useDateTime()
  const { copied, copy } = useCopyFeedback<"serverId">()
  // 按新时区重建期间每隔几秒刷新，重建完成后停止。
  const overview = useResource(resourceKeys.platformOverview(), (signal) => getPlatformOverview(signal), {
    staleTime: 0,
    refetchInterval: (current) => (current?.statsRebuilding ? 3000 : false),
  })
  const settings = useResource(resourceKeys.platformSettings(), (signal) => getPlatformSettings(signal), {
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  const data = overview.data
  // 有效授权到期后重新读取概览，授权状态与工作区上限随之更新。
  useRefreshAtLicenseExpiry(data?.license, overview.refresh)
  const count = useMemo(() => new Intl.NumberFormat(i18n.resolvedLanguage), [i18n.resolvedLanguage])
  const dayLabel = useMemo(
    () => new Intl.DateTimeFormat(i18n.resolvedLanguage, { month: "numeric", day: "numeric", timeZone: "UTC" }),
    [i18n.resolvedLanguage],
  )

  /** 复制服务器标识，失败时提示手动复制。 */
  async function copyServerID(value: string) {
    if (!(await copy(value, "serverId"))) toast.error(t("overview.copyError"))
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("overview.title")} description={t("overview.description")} />
      <PageContent variant="form">
        <ResourceContent resources={[overview, settings]} errorMessage={t("overview.loadError")}>
          {data && settings.data ? (
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

              <ReportSection
                title={data.statsRebuilding ? `${t("overview.trendTitle")} · ${t("overview.statsRebuilding")}` : t("overview.trendTitle")}
              >
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
              </ReportSection>

              <FieldGroup className="grid">
                <TimeZoneField settings={settings.data} />
                <LicenseField license={data.license} />
                <Field>
                  <FieldLabel htmlFor="platform-server-id">{t("overview.serverId")}</FieldLabel>
                  <div className="flex items-center gap-2">
                    <Input id="platform-server-id" value={data.serverId} readOnly className="font-mono text-muted-foreground" />
                    <Button type="button" variant="outline" className="h-11 shrink-0" onClick={() => void copyServerID(data.serverId)}>
                      {copied === "serverId" ? t("common:actions.copied") : t("common:actions.copy")}
                    </Button>
                  </div>
                  <FieldDescription>{t("overview.serverIdHelp")}</FieldDescription>
                </Field>
                <TelemetryField settings={settings.data} />
                <div className="grid gap-6 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="platform-installed-at">{t("overview.installedAt")}</FieldLabel>
                    <Input id="platform-installed-at" value={formatDateTime(data.installedAt)} readOnly className="text-muted-foreground" />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="platform-workspace-limit">{t("overview.workspaceLimit")}</FieldLabel>
                    <Input
                      id="platform-workspace-limit"
                      value={data.capabilities.workspaceLimit === 0 ? t("overview.unlimited") : data.capabilities.workspaceLimit}
                      readOnly
                      className="text-muted-foreground tabular-nums"
                    />
                  </Field>
                </div>
              </FieldGroup>
            </div>
          ) : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 只读的授权状态：有效时显示客户与到期时间，临近到期或已到期时在下方提醒，可进入授权页管理。 */
function LicenseField({ license }: { license: License }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const help = useLicenseStatusHelp(license)
  const statusLabels: Record<string, string> = {
    [LicenseStatus.LicenseStatusNone]: t("license.statuses.none"),
    [LicenseStatus.LicenseStatusActive]: t("license.statuses.active"),
    [LicenseStatus.LicenseStatusExpired]: t("license.statuses.expired"),
  }
  const value =
    license.status === LicenseStatus.LicenseStatusNone
      ? statusLabels[license.status]
      : t("overview.licenseSummary", {
          status: statusLabels[license.status],
          customer: license.customer,
          expiresAt: license.expiresAt ? formatDateTime(license.expiresAt) : "",
        })

  return (
    <Field>
      <FieldLabel htmlFor="platform-license">{t("overview.license")}</FieldLabel>
      <div className="flex items-center gap-2">
        <Input id="platform-license" value={value} readOnly className="text-muted-foreground" />
        <Button asChild variant="outline" className="h-11 shrink-0">
          <Link to="/settings/platform/license">{t("overview.manageLicense")}</Link>
        </Button>
      </div>
      {help ? <FieldDescription>{help}</FieldDescription> : null}
    </Field>
  )
}

/** 平台时区选择框，选择后立即保存并刷新概览中的活跃数据。 */
function TimeZoneField({ settings }: { settings: PlatformSettings }) {
  const { t } = useTranslation("platform")
  const invalidate = useResourceInvalidator()
  const timeZones = useMemo(() => supportedTimeZones(settings.timeZone), [settings.timeZone])
  const form = useForm<TimeZoneFormValues>({
    resolver: zodResolver(timeZoneSchema),
    shouldUseNativeValidation: true,
    defaultValues: { timeZone: settings.timeZone },
  })
  const { submit } = useFormSave({
    form,
    schema: timeZoneSchema,
    autoSave: true,
    save: async (values) => {
      const saved = await updatePlatformTimeZone(values)
      void invalidate(resourceKeys.platformOverview())
      return saved
    },
    savedValues: (saved) => ({ timeZone: saved.timeZone }),
    errorMessage: t("overview.timeZoneSaveError"),
    errorFields: ["timeZone"],
    logLabel: "保存平台时区",
  })

  return (
    <form onSubmit={form.handleSubmit(submit)} noValidate>
      <Controller
        name="timeZone"
        control={form.control}
        render={({ field, fieldState }) => (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor={field.name} required>
              {t("overview.timeZone")}
            </FieldLabel>
            <NativeSelect {...field} id={field.name} aria-invalid={fieldState.invalid}>
              {timeZones.map((timeZone) => (
                <option key={timeZone} value={timeZone}>
                  {timeZone}
                </option>
              ))}
            </NativeSelect>
            <FieldDescription>{t("overview.timeZoneHelp")}</FieldDescription>
          </Field>
        )}
      />
    </form>
  )
}

/** 运行指标与错误上报开关，切换后立即保存。 */
function TelemetryField({ settings }: { settings: PlatformSettings }) {
  const { t } = useTranslation("platform")
  const form = useForm<TelemetryFormValues>({
    resolver: zodResolver(telemetrySchema),
    shouldUseNativeValidation: true,
    defaultValues: { telemetryEnabled: settings.telemetryEnabled },
  })
  const { submit } = useFormSave({
    form,
    schema: telemetrySchema,
    autoSave: true,
    save: (values) => updatePlatformTelemetry(values),
    savedValues: (saved) => ({ telemetryEnabled: saved.telemetryEnabled }),
    errorMessage: t("overview.telemetrySaveError"),
    errorFields: ["telemetryEnabled"],
    logLabel: "保存上报开关",
  })

  return (
    <form onSubmit={form.handleSubmit(submit)} noValidate>
      <Controller
        name="telemetryEnabled"
        control={form.control}
        render={({ field }) => (
          <SwitchField
            id={field.name}
            name={field.name}
            label={t("overview.telemetry")}
            description={t("overview.telemetryHelp")}
            checked={field.value}
            onBlur={field.onBlur}
            onCheckedChange={field.onChange}
            ref={field.ref}
          />
        )}
      />
    </form>
  )
}
