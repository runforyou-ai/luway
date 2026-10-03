/** 平台设置的授权页：展示授权状态、期限与授予的能力；未激活时在线输入激活码或离线粘贴授权码激活，已有授权时向授权服务同步或经弹窗离线更换授权码。 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  activateLicense,
  activateLicenseOnline,
  getLicense,
  isApiError,
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
import {
  createLicenseActivationSchema,
  createOnlineActivationSchema,
  type LicenseActivationFormValues,
  type OnlineActivationFormValues,
} from "@/features/settings/platform/platform-license-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
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
          {license.data ? <LicenseDetails license={license.data} /> : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 返回授权状态下方的说明：未激活时说明免费范围，临近到期或已到期时提醒续期。 */
function useStatusHelp(license: License) {
  const { t } = useTranslation("platform")
  if (license.status === LicenseStatus.LicenseStatusNone) {
    return t("license.statusHelp.none", { count: license.capabilities.workspaceLimit })
  }
  if (license.status === LicenseStatus.LicenseStatusExpired) return t("license.statusHelp.expired")
  const remainingDays = Math.max(1, Math.ceil((new Date(license.expiresAt ?? 0).getTime() - Date.now()) / 86_400_000))
  return remainingDays <= renewalReminderDays ? t("license.statusHelp.expiring", { count: remainingDays }) : null
}

/** 返回授权变化后刷新授权、平台概览与工作区列表的函数。 */
function useLicenseChanged() {
  const invalidate = useResourceInvalidator()
  return useCallback(() => {
    void invalidate(resourceKeys.license())
    void invalidate(resourceKeys.platformOverview())
    void invalidate(resourceKeys.workspaces())
  }, [invalidate])
}

/** 只读展示当前授权，有效期内展示授予的能力；已有授权时可同步或经弹窗离线更换授权码，未激活时选择在线激活或离线授权。 */
function LicenseDetails({ license }: { license: License }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const licenseChanged = useLicenseChanged()
  const [replacing, setReplacing] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const replaceButtonRef = useRef<HTMLButtonElement>(null)
  const statusHelp = useStatusHelp(license)
  const licensed = license.status !== LicenseStatus.LicenseStatusNone
  const active = license.status === LicenseStatus.LicenseStatusActive
  const statusLabels: Record<string, string> = {
    [LicenseStatus.LicenseStatusNone]: t("license.statuses.none"),
    [LicenseStatus.LicenseStatusActive]: t("license.statuses.active"),
    [LicenseStatus.LicenseStatusExpired]: t("license.statuses.expired"),
  }

  // 平台没有授权时关闭更换弹窗。
  useEffect(() => {
    if (!licensed) setReplacing(false)
  }, [licensed])

  /** 向授权服务同步授权，按签发时间是否变化提示已更新或已是最新。 */
  async function sync() {
    setSyncing(true)
    try {
      const synced = await syncLicense()
      toast.success(synced.issuedAt === license.issuedAt ? t("license.syncUnchanged") : t("license.syncUpdated"))
      licenseChanged()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("同步授权失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, []) : t("license.syncError"))
    } finally {
      setSyncing(false)
    }
  }

  return (
    <div className="w-full space-y-9">
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
            {active ? (
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
            <Button type="button" className="touch:min-h-11 touch:flex-1" disabled={syncing} onClick={() => void sync()}>
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
          <TabsContent value="offline" className="mt-6 space-y-6">
            <ServerIDField serverId={license.serverId} />
            <LicenseCodeForm />
          </TabsContent>
        </Tabs>
      )}
    </div>
  )
}

/** 只读展示服务器标识并提供复制，离线申请授权码时使用。 */
function ServerIDField({ serverId }: { serverId: string }) {
  const { t } = useTranslation(["platform", "common"])
  const { copied, copy } = useCopyFeedback<"serverId">()

  /** 复制服务器标识，失败时提示手动复制。 */
  async function copyServerID() {
    if (!(await copy(serverId, "serverId"))) toast.error(t("overview.copyError"))
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

/** 输入激活码经授权服务在线激活；激活成功后刷新授权、平台概览与工作区列表。 */
function OnlineActivationForm() {
  const { t } = useTranslation(["platform", "common"])
  const navigate = useNavigate()
  const licenseChanged = useLicenseChanged()
  const schema = useMemo(() => createOnlineActivationSchema(t), [t])
  const form = useForm<OnlineActivationFormValues>({
    resolver: zodResolver(schema),
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
      if (recoverSession(error, navigate)) return
      console.warn("在线激活授权失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, ["activationCode"]) : t("license.activateError"))
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

/** 粘贴授权码离线激活或替换授权；激活成功后刷新授权、平台概览与工作区列表。 */
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
  const navigate = useNavigate()
  const licenseChanged = useLicenseChanged()
  const schema = useMemo(() => createLicenseActivationSchema(t), [t])
  const form = useForm<LicenseActivationFormValues>({
    resolver: zodResolver(schema),
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
      if (recoverSession(error, navigate)) return
      console.warn("激活授权失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, ["licenseCode"]) : t("license.activateError"))
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
