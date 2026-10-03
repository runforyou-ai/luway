/** 部署设置的实例授权页：展示授权状态、期限与授予的能力，粘贴授权码激活或替换授权。 */
import { useEffect, useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  activateInstanceLicense,
  getInstanceLicense,
  isApiError,
  LicenseStatus,
  loadInstallationStatus,
  type InstanceLicense,
} from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  createLicenseActivationSchema,
  type LicenseActivationFormValues,
} from "@/features/settings/deployment/deployment-license-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { applyBrand } from "@/lib/brand"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 授权到期前开始提醒续期的天数。 */
const renewalReminderDays = 30

/** 浏览器定时器允许的最大延迟毫秒数。 */
const maxTimerDelay = 2_147_483_647

/** 每次进入页面读取最新授权状态后渲染授权信息与激活表单，并按授权同步当前品牌。 */
export function DeploymentLicensePage() {
  const { t } = useTranslation("deployment")
  const license = useResource(resourceKeys.instanceLicense(), (signal) => getInstanceLicense(signal), { staleTime: 0 })
  const status = license.data?.status
  const expiresAt = license.data?.expiresAt
  const customBranding = license.data?.capabilities.customBranding
  const refreshLicense = license.refresh

  // 授权决定部署品牌是否生效，读到的授权状态、期限或品牌能力变化时同步当前品牌。
  useEffect(() => {
    if (!status) return
    void loadInstallationStatus().then(
      (installation) => applyBrand(installation.brand),
      (error: unknown) => console.warn("同步品牌失败", error),
    )
  }, [status, expiresAt, customBranding])

  // 有效授权到期后重新读取授权状态。
  useEffect(() => {
    if (status !== LicenseStatus.LicenseStatusActive || !expiresAt) return
    let timer = 0
    // 到期时间超出单次定时器上限时分段续设。
    const schedule = () => {
      const delay = Math.max(new Date(expiresAt).getTime() - Date.now(), 0) + 1000
      timer = delay > maxTimerDelay ? window.setTimeout(schedule, maxTimerDelay) : window.setTimeout(() => void refreshLicense(), delay)
    }
    schedule()
    return () => window.clearTimeout(timer)
  }, [status, expiresAt, refreshLicense])

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("license.title")} description={t("license.description")} />
      <PageContent variant="form">
        <ResourceContent resources={license} errorMessage={t("license.loadError")}>
          {license.data ? <LicenseForm license={license.data} /> : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 返回授权状态下方的说明：未激活时说明免费范围，临近到期或已到期时提醒续期。 */
function useStatusHelp(license: InstanceLicense) {
  const { t } = useTranslation("deployment")
  if (license.status === LicenseStatus.LicenseStatusNone) {
    return t("license.statusHelp.none", { count: license.capabilities.workspaceLimit })
  }
  if (license.status === LicenseStatus.LicenseStatusExpired) return t("license.statusHelp.expired")
  const remainingDays = Math.max(1, Math.ceil((new Date(license.expiresAt ?? 0).getTime() - Date.now()) / 86_400_000))
  return remainingDays <= renewalReminderDays ? t("license.statusHelp.expiring", { count: remainingDays }) : null
}

/** 只读展示当前授权，有效期内展示授予的能力，并提交新的授权码；激活成功后刷新授权、部署概况与工作区列表。 */
function LicenseForm({ license }: { license: InstanceLicense }) {
  const { t } = useTranslation("deployment")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const { formatDateTime } = useDateTime()
  const statusHelp = useStatusHelp(license)
  const schema = useMemo(() => createLicenseActivationSchema(t), [t])
  const form = useForm<LicenseActivationFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { licenseCode: "" },
  })
  useFormLifetime(form.formState.isDirty)
  const licensed = license.status !== LicenseStatus.LicenseStatusNone
  const statusLabels: Record<string, string> = {
    [LicenseStatus.LicenseStatusNone]: t("license.statuses.none"),
    [LicenseStatus.LicenseStatusActive]: t("license.statuses.active"),
    [LicenseStatus.LicenseStatusExpired]: t("license.statuses.expired"),
  }

  /** 提交授权码。 */
  async function activate(values: LicenseActivationFormValues) {
    try {
      await activateInstanceLicense({ licenseCode: values.licenseCode })
      form.reset()
      toast.success(t("license.activateSuccess"))
      void invalidate(resourceKeys.instanceLicense())
      void invalidate(resourceKeys.deploymentOverview())
      void invalidate(resourceKeys.workspaces())
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("激活实例授权失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, ["licenseCode"]) : t("license.activateError"))
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form className="w-full space-y-9" aria-label={t("license.formLabel")} onSubmit={form.handleSubmit(activate)} noValidate>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="license-status">{t("license.status")}</FieldLabel>
          <Input id="license-status" value={statusLabels[license.status] ?? ""} readOnly className="text-muted-foreground" />
          {statusHelp ? <FieldDescription>{statusHelp}</FieldDescription> : null}
        </Field>
        {licensed ? (
          <>
            <div className="grid gap-6 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="license-customer">{t("license.customer")}</FieldLabel>
                <Input id="license-customer" value={license.customer} readOnly className="text-muted-foreground" />
              </Field>
              <Field>
                <FieldLabel htmlFor="license-expires-at">{t("license.expiresAt")}</FieldLabel>
                <Input
                  id="license-expires-at"
                  value={license.expiresAt ? formatDateTime(license.expiresAt) : ""}
                  readOnly
                  className="text-muted-foreground"
                />
              </Field>
            </div>
            {license.status === LicenseStatus.LicenseStatusActive ? (
              <div className="grid gap-6 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="license-workspace-limit">{t("license.workspaceLimit")}</FieldLabel>
                  <Input
                    id="license-workspace-limit"
                    value={license.capabilities.workspaceLimit === 0 ? t("overview.unlimited") : license.capabilities.workspaceLimit}
                    readOnly
                    className="text-muted-foreground tabular-nums"
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="license-custom-branding">{t("license.customBranding")}</FieldLabel>
                  <Input
                    id="license-custom-branding"
                    value={license.capabilities.customBranding ? t("license.allowed") : t("license.notAllowed")}
                    readOnly
                    className="text-muted-foreground"
                  />
                </Field>
              </div>
            ) : null}
            <Field>
              <FieldLabel htmlFor="license-id">{t("license.licenseId")}</FieldLabel>
              <Input id="license-id" value={license.licenseId} readOnly className="font-mono text-muted-foreground" />
            </Field>
          </>
        ) : null}
        <Controller
          name="licenseCode"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {licensed ? t("license.newLicenseCode") : t("license.licenseCode")}
              </FieldLabel>
              <Textarea
                {...field}
                id={field.name}
                rows={5}
                spellCheck={false}
                autoComplete="off"
                className="font-mono text-xs break-all"
                aria-invalid={fieldState.invalid}
                required
              />
              <FieldDescription>{t("license.licenseCodeHelp")}</FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
      <div className="flex justify-end">
        <Button type="submit" className="touch:min-h-11 touch:w-full" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("license.activating") : t("license.activate")}
        </Button>
      </div>
    </form>
  )
}
