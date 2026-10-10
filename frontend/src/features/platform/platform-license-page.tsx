/** 平台设置的授权页：展示服务器标识、授权状态、与授权服务的同步结果、期限与授予的能力；未激活时在线输入激活码或离线粘贴授权码激活，已有授权时向授权服务同步或经弹窗离线更换授权码。 */
import { useCallback, useEffect, useRef, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  activateLicense,
  activateLicenseOnline,
  getLicense,
  LicenseStatus,
  loadInstallationStatus,
  syncLicense,
  type License,
} from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { licenseRemainingDays, renewalReminderDays } from "@/features/platform/license-reminder"
import {
  licenseActivationSchema,
  onlineActivationSchema,
  type LicenseActivationFormValues,
  type OnlineActivationFormValues,
} from "@/features/platform/platform-license-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { useDateTime } from "@/hooks/use-date-time"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { applyBrand } from "@/lib/brand"
import { zodResolver } from "@/lib/zod-resolver"

/** 浏览器定时器允许的最大延迟毫秒数。 */
const maxTimerDelay = 2_147_483_647

/** 每次进入页面读取最新授权状态后渲染授权信息，并按授权同步当前品牌。 */
export function PlatformLicensePage() {
  const { t } = useTranslation("platform")
  const license = useResource(resourceKeys.license(), (signal) => getLicense(signal), { staleTime: 0 })
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

  useRefreshAtLicenseExpiry(license.data, refreshLicense)

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("license.title")} description={t("license.description")} />
      <PageContent variant="form">
        <ResourceContent resources={license} errorMessage={t("license.loadError")}>
          {license.data ? <LicenseDetails license={license.data} /> : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 有效授权到期后调用 refresh 重新读取，授权尚未读取或不是有效状态时不计时。 */
function useRefreshAtLicenseExpiry(license: License | undefined, refresh: () => unknown) {
  const status = license?.status
  const expiresAt = license?.expiresAt
  useEffect(() => {
    if (status !== LicenseStatus.Active || !expiresAt) return
    let timer = 0
    // 到期时间超出单次定时器上限时分段续设。
    const schedule = () => {
      const delay = Math.max(new Date(expiresAt).getTime() - Date.now(), 0) + 1000
      timer = delay > maxTimerDelay ? window.setTimeout(schedule, maxTimerDelay) : window.setTimeout(() => void refresh(), delay)
    }
    schedule()
    return () => window.clearTimeout(timer)
  }, [status, expiresAt, refresh])
}

/** 返回授权状态下方的说明：未激活时说明免费范围，临近到期或已到期时提醒续期。 */
function useLicenseStatusHelp(license: License) {
  const { t } = useTranslation("platform")
  if (license.status === LicenseStatus.None) {
    return t("license.statusHelp.none", { count: license.capabilities.workspaceLimit })
  }
  if (license.status === LicenseStatus.Expired) return t("license.statusHelp.expired")
  const remainingDays = licenseRemainingDays(license)
  return remainingDays <= renewalReminderDays ? t("license.statusHelp.expiring", { count: remainingDays }) : null
}

/** 返回授权变化后刷新授权与工作区列表的函数。 */
function useLicenseChanged() {
  const invalidate = useResourceInvalidator()
  return useCallback(() => {
    void invalidate(resourceKeys.license())
    void invalidate(resourceKeys.workspaces())
  }, [invalidate])
}

/** 只读展示当前授权，有效期内展示授予的能力；已有授权时可同步或经弹窗离线更换授权码，未激活时选择在线激活或离线授权。 */
function LicenseDetails({ license }: { license: License }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const reportError = useRequestErrorReporter()
  const licenseChanged = useLicenseChanged()
  const [replacing, setReplacing] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  // 向授权服务同步授权：查不到本服务器授权时提示联系服务商，其余按发起时的签发时间是否变化提示已更新或已是最新。
  const syncRequest = useMutation({
    mutationFn: (_issuedAt: License["issuedAt"]) => syncLicense(),
    onSuccess: (synced, issuedAt) => {
      if (synced.controlMissingAt) toast.warning(t("license.controlMissing"))
      else toast.success(synced.issuedAt === issuedAt ? t("license.syncUnchanged") : t("license.syncUpdated"))
      licenseChanged()
    },
    onError: (error) => reportError(error, { log: "同步授权", fallback: t("license.syncError") }),
  })
  const syncing = syncRequest.isPending
  const replaceButtonRef = useRef<HTMLButtonElement>(null)
  const statusHelp = useLicenseStatusHelp(license)
  const licensed = license.status !== LicenseStatus.None
  const active = license.status === LicenseStatus.Active
  const statusLabels: Record<string, string> = {
    [LicenseStatus.None]: t("license.statuses.none"),
    [LicenseStatus.Active]: t("license.statuses.active"),
    [LicenseStatus.Expired]: t("license.statuses.expired"),
  }

  // 平台没有授权时关闭更换弹窗。
  useEffect(() => {
    if (!licensed) setReplacing(false)
  }, [licensed])

  return (
    <div className="w-full space-y-9">
      <FieldGroup>
        <ServerIDField serverId={license.serverId} />
        <Field>
          <FieldLabel htmlFor="license-status">{t("license.status")}</FieldLabel>
          <Input id="license-status" value={statusLabels[license.status] ?? ""} readOnly className="text-muted-foreground" />
          {statusHelp ? <FieldDescription>{statusHelp}</FieldDescription> : null}
          {licensed && license.controlMissingAt ? (
            <FieldDescription className="text-destructive">{t("license.controlMissing")}</FieldDescription>
          ) : null}
        </Field>
        <Field>
          <FieldLabel htmlFor="license-sync">{t("license.syncStatus")}</FieldLabel>
          <Input
            id="license-sync"
            value={
              license.sync.failedAt
                ? t("license.syncStatuses.failed", { time: formatDateTime(license.sync.failedAt) })
                : license.sync.syncedAt
                  ? t("license.syncStatuses.succeeded", { time: formatDateTime(license.sync.syncedAt) })
                  : t("license.syncStatuses.never")
            }
            readOnly
            className="text-muted-foreground"
          />
          {license.sync.failedAt ? (
            <FieldDescription className="text-destructive break-all">
              {license.sync.syncedAt
                ? t("license.syncFailedHelp", { error: license.sync.error, time: formatDateTime(license.sync.syncedAt) })
                : license.sync.error}
            </FieldDescription>
          ) : (
            <FieldDescription>{t("license.syncHelp")}</FieldDescription>
          )}
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
            {active ? (
              <div className="grid gap-6 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="license-workspace-limit">{t("license.workspaceLimit")}</FieldLabel>
                  <Input
                    id="license-workspace-limit"
                    value={license.capabilities.workspaceLimit === 0 ? t("license.unlimited") : license.capabilities.workspaceLimit}
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
                <Field>
                  <FieldLabel htmlFor="license-push-relay">{t("license.pushRelay")}</FieldLabel>
                  <Input
                    id="license-push-relay"
                    value={license.capabilities.pushRelay ? t("license.allowed") : t("license.notAllowed")}
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
      </FieldGroup>
      {licensed ? (
        <>
          <div className="flex justify-end gap-2">
            <Button
              ref={replaceButtonRef}
              type="button"
              variant="outline"
              className="touch:min-h-11 touch:flex-1"
              disabled={syncing}
              onClick={() => setReplacing(true)}
            >
              {t("license.replace")}
            </Button>
            <Button type="button" className="touch:min-h-11 touch:flex-1" disabled={syncing} onClick={() => syncRequest.mutate(license.issuedAt)}>
              {syncing ? <LoaderCircleIcon className="animate-spin" /> : null}
              {syncing ? t("license.syncing") : t("license.sync")}
            </Button>
          </div>
          <Dialog open={replacing} onOpenChange={(open) => !submitting && setReplacing(open)}>
            <DialogContent
              closeDisabled={submitting}
              onCloseAutoFocus={(event) => {
                // 关闭弹窗后焦点回到更换按钮。
                event.preventDefault()
                replaceButtonRef.current?.focus()
              }}
            >
              <DialogHeader>
                <DialogTitle>{t("license.replaceTitle")}</DialogTitle>
                <DialogDescription>{t("license.replaceDescription")}</DialogDescription>
              </DialogHeader>
              {replacing ? (
                <LicenseCodeForm
                  onCancel={() => setReplacing(false)}
                  onActivated={() => setReplacing(false)}
                  onSubmittingChange={setSubmitting}
                />
              ) : null}
            </DialogContent>
          </Dialog>
        </>
      ) : (
        <Tabs defaultValue="online">
          <TabsList>
            <TabsTrigger value="online">{t("license.methods.online")}</TabsTrigger>
            <TabsTrigger value="offline">{t("license.methods.offline")}</TabsTrigger>
          </TabsList>
          <TabsContent value="online" className="mt-6">
            <OnlineActivationForm />
          </TabsContent>
          <TabsContent value="offline" className="mt-6">
            <LicenseCodeForm />
          </TabsContent>
        </Tabs>
      )}
    </div>
  )
}

/** 只读展示服务器标识并提供复制，申请授权码与联系服务商时使用。 */
function ServerIDField({ serverId }: { serverId: string }) {
  const { t } = useTranslation(["platform", "common"])
  const { copied, copy } = useCopyFeedback<"serverId">()

  /** 复制服务器标识，失败时提示手动复制。 */
  async function copyServerID() {
    if (!(await copy(serverId, "serverId"))) toast.error(t("license.copyError"))
  }

  return (
    <Field>
      <FieldLabel htmlFor="license-server-id">{t("license.serverId")}</FieldLabel>
      <div className="flex items-center gap-2">
        <Input id="license-server-id" value={serverId} readOnly className="font-mono text-muted-foreground" />
        <Button type="button" variant="outline" className="h-11 shrink-0" onClick={() => void copyServerID()}>
          {copied === "serverId" ? t("common:actions.copied") : t("common:actions.copy")}
        </Button>
      </div>
      <FieldDescription>{t("license.serverIdHelp")}</FieldDescription>
    </Field>
  )
}

/** 输入激活码经授权服务在线激活；激活成功后刷新授权、工作区列表。 */
function OnlineActivationForm() {
  const { t } = useTranslation(["platform", "common"])
  const reportError = useRequestErrorReporter()
  const licenseChanged = useLicenseChanged()
  const form = useForm<OnlineActivationFormValues>({
    resolver: zodResolver(onlineActivationSchema),
    shouldUseNativeValidation: true,
    defaultValues: { activationCode: "" },
  })
  useFormLifetime(form.formState.isDirty)

  /** 提交激活码。 */
  async function activate(values: OnlineActivationFormValues) {
    try {
      await activateLicenseOnline({ activationCode: values.activationCode })
      form.reset()
      toast.success(t("license.activateSuccess"))
      licenseChanged()
    } catch (error) {
      reportError(error, { log: "在线激活授权", fallback: t("license.activateError"), fields: ["activationCode"] })
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form className="w-full space-y-9" aria-label={t("license.methods.online")} onSubmit={form.handleSubmit(activate)} noValidate>
      <FieldGroup>
        <Controller
          name="activationCode"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("license.activationCode")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                aria-invalid={fieldState.invalid}
                required
              />
              <FieldDescription>{t("license.activationCodeHelp")}</FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
      <div className="flex justify-end">
        <Button type="submit" className="touch:min-h-11 touch:flex-1" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("license.activating") : t("license.activate")}
        </Button>
      </div>
    </form>
  )
}

/** 粘贴授权码离线激活或替换授权；激活成功后刷新授权、工作区列表。 */
function LicenseCodeForm({
  onCancel,
  onActivated,
  onSubmittingChange,
}: {
  onCancel?: () => void
  onActivated?: () => void
  onSubmittingChange?: (submitting: boolean) => void
}) {
  const { t } = useTranslation(["platform", "common"])
  const reportError = useRequestErrorReporter()
  const licenseChanged = useLicenseChanged()
  const form = useForm<LicenseActivationFormValues>({
    resolver: zodResolver(licenseActivationSchema),
    shouldUseNativeValidation: true,
    defaultValues: { licenseCode: "" },
  })
  useFormLifetime(form.formState.isDirty)

  /** 提交授权码。 */
  async function activate(values: LicenseActivationFormValues) {
    try {
      await activateLicense({ licenseCode: values.licenseCode })
      form.reset()
      toast.success(t("license.activateSuccess"))
      licenseChanged()
      onActivated?.()
    } catch (error) {
      reportError(error, { log: "激活授权", fallback: t("license.activateError"), fields: ["licenseCode"] })
    }
  }

  const { isSubmitting } = form.formState

  // 向弹窗同步提交状态，提交期间停用关闭入口；表单卸载时恢复。
  useEffect(() => {
    onSubmittingChange?.(isSubmitting)
    return () => onSubmittingChange?.(false)
  }, [isSubmitting, onSubmittingChange])

  return (
    <form className="w-full space-y-9" aria-label={t("license.methods.offline")} onSubmit={form.handleSubmit(activate)} noValidate>
      <FieldGroup>
        <Controller
          name="licenseCode"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("license.licenseCode")}
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
      <div className="flex justify-end gap-2">
        {onCancel ? (
          <Button type="button" variant="outline" className="touch:min-h-11 touch:flex-1" disabled={isSubmitting} onClick={onCancel}>
            {t("common:actions.cancel")}
          </Button>
        ) : null}
        <Button type="submit" className="touch:min-h-11 touch:flex-1" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("license.activating") : t("license.activate")}
        </Button>
      </div>
    </form>
  )
}
