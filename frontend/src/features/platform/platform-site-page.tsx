/** 站点设置页：一次读取与保存部署名称、时区、注册与创建、访问地址与证书、品牌。 */
import { useMemo, useRef, useState } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  getPlatformSite,
  loadInstallationStatus,
  PlatformCertificateSource,
  RegistrationPolicy,
  updatePlatformSite,
  type PlatformSite,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { PageContent } from "@/components/page-content"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import { AdminPage } from "@/features/admin/admin-page"
import { createSiteSchema, isHTTPSAddress, type SiteFormValues } from "@/features/platform/platform-site-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { applyBrand } from "@/lib/brand"
import { supportedTimeZones } from "@/lib/time-zones"
import { zodResolver } from "@/lib/zod-resolver"

/** 网站图标的最大字节数，与服务端校验一致。 */
const brandIconMaxBytes = 512 * 1024

/** 每次进入页面读取最新站点设置后渲染长表单；未授权时额度与品牌只读。 */
export function PlatformSitePage() {
  const { t } = useTranslation("platform")
  const site = useResource(resourceKeys.platformSite(), (signal) => getPlatformSite(signal), {
    gcTime: 0,
    refetchOnWindowFocus: false,
  })

  return (
    <AdminPage title={t("site.title")} description={t("site.description")}>
      <PageContent variant="form">
        <ResourceContent resources={site} errorMessage={t("site.loadError")}>
          {site.data ? <SiteForm site={site.data} /> : null}
        </ResourceContent>
      </PageContent>
    </AdminPage>
  )
}

/** 把站点设置转为表单初始值。 */
function siteValues(site: PlatformSite): SiteFormValues {
  const locales = Object.keys(__BUILD_BRAND__.names ?? {}).sort()
  return {
    name: site.name,
    timeZone: site.timeZone,
    registrationPolicy: site.registrationPolicy as SiteFormValues["registrationPolicy"],
    workspaceCreationQuota: site.workspaceCreationQuota,
    publicURL: site.publicURL,
    source: site.certificate.source,
    certificate: site.certificate.certificate,
    privateKey: site.certificate.privateKey,
    names: Object.fromEntries(locales.map((locale) => [locale, site.branding.names[locale] ?? ""])),
    sdkName: site.branding.sdkName,
    icon: site.branding.icon ?? "",
  }
}

/** 站点设置长表单：字段连续展示，底部一次保存。 */
function SiteForm({ site }: { site: PlatformSite }) {
  const { t } = useTranslation("platform")
  const invalidate = useResourceInvalidator()
  const { formatDateTime } = useDateTime()
  const licensed = site.brandingLicensed
  const locales = useMemo(() => Object.keys(__BUILD_BRAND__.names ?? {}).sort(), [])
  const timeZones = useMemo(() => supportedTimeZones(site.timeZone), [site.timeZone])
  const schema = useMemo(() => createSiteSchema(t, site.httpsServers), [t, site.httpsServers])
  const form = useForm<SiteFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: siteValues(site),
  })
  const https = isHTTPSAddress(form.watch("publicURL"))
  const source = form.watch("source")
  const registrationPolicy = form.watch("registrationPolicy")
  const status = site.certificateStatus
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: async (values) => {
      const saved = await updatePlatformSite({
        name: values.name,
        timeZone: values.timeZone,
        registrationPolicy: values.registrationPolicy,
        workspaceCreationQuota: values.workspaceCreationQuota,
        publicURL: values.publicURL,
        certificate: {
          source: values.source,
          certificate: values.certificate,
          privateKey: values.privateKey,
        },
        branding: { names: values.names, sdkName: values.sdkName, icon: values.icon },
      })
      return saved
    },
    savedValues: (saved) => siteValues(saved),
    onSubmitted: () => {
      toast.success(t("site.saved"))
      void invalidate(resourceKeys.platformSite())
      void invalidate(resourceKeys.installationStatus())
      void invalidate(resourceKeys.platformOverview())
      void invalidate(resourceKeys.workspaces())
      void loadInstallationStatus().then(
        (installation) => applyBrand(installation.brand),
        (error: unknown) => console.warn("同步品牌失败", error),
      )
    },
    errorMessage: t("site.saveError"),
    errorFields: [
      "name",
      "timeZone",
      "registrationPolicy",
      "workspaceCreationQuota",
      "publicURL",
      "certificateSource",
      "certificate",
      "privateKey",
      "names",
      "sdkName",
      "icon",
    ],
    logLabel: "保存站点设置",
  })

  return (
    <form className="space-y-9" aria-label={t("site.title")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <FormInputField name="name" control={form.control} label={t("deployment.name")} description={t("deployment.nameHelp")} required={false} />
        <Controller
          name="timeZone"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="site-time-zone" required>
                {t("deployment.timeZone")}
              </FieldLabel>
              <NativeSelect {...field} id="site-time-zone" aria-invalid={fieldState.invalid}>
                {timeZones.map((timeZone) => (
                  <option key={timeZone} value={timeZone}>
                    {timeZone}
                  </option>
                ))}
              </NativeSelect>
              <FieldDescription>{t("deployment.timeZoneHelp")}</FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="registrationPolicy"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("registration.registrationPolicy")}
              </FieldLabel>
              <NativeSelect {...field} id={field.name} aria-invalid={fieldState.invalid}>
                <option value={RegistrationPolicy.InvitationOnly}>
                  {t("registration.registrationPolicies.invitationOnly")}
                </option>
                <option value={RegistrationPolicy.Open}>{t("registration.registrationPolicies.open")}</option>
              </NativeSelect>
              <FieldDescription>
                {registrationPolicy === RegistrationPolicy.Open
                  ? t("registration.registrationHelp.open")
                  : t("registration.registrationHelp.invitationOnly")}
              </FieldDescription>
              <FieldDescription>{t("registration.inviteVsRegisterHelp")}</FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="workspaceCreationQuota"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("registration.workspaceCreationQuota")}
              </FieldLabel>
              <Input
                id={field.name}
                type="number"
                min={licensed ? site.workspaceCreationQuota : 0}
                step={1}
                inputMode="numeric"
                disabled={!licensed}
                aria-invalid={fieldState.invalid}
                className="tabular-nums"
                name={field.name}
                ref={field.ref}
                value={Number.isFinite(field.value) ? field.value : 0}
                onBlur={field.onBlur}
                onChange={(event) => {
                  const next = event.target.valueAsNumber
                  field.onChange(Number.isFinite(next) ? next : 0)
                }}
              />
              <FieldDescription>
                {licensed ? t("registration.workspaceCreationQuotaHelp") : t("registration.workspaceCreationQuotaLocked")}
              </FieldDescription>
            </Field>
          )}
        />
        <FormInputField
          name="publicURL"
          control={form.control}
          label={t("deployment.publicURL")}
          description={t(https && !site.httpsServers ? "deployment.address.proxyHelp" : "deployment.publicURLHelp")}
          type="url"
        />
        {https && site.httpsServers ? (
          <>
            <Controller
              name="source"
              control={form.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <FieldLabel htmlFor="site-certificate-source" required>
                    {t("deployment.address.source")}
                  </FieldLabel>
                  <NativeSelect {...field} id="site-certificate-source" aria-invalid={fieldState.invalid}>
                    <option value={PlatformCertificateSource.ACME}>{t("deployment.address.sourceACME")}</option>
                    <option value={PlatformCertificateSource.Upload}>{t("deployment.address.sourceUpload")}</option>
                  </NativeSelect>
                  <FieldDescription>
                    {t(source === PlatformCertificateSource.ACME ? "deployment.address.sourceACMEHelp" : "deployment.address.sourceUploadHelp")}
                  </FieldDescription>
                </Field>
              )}
            />
            {source === PlatformCertificateSource.Upload ? (
              <>
                <Controller
                  name="certificate"
                  control={form.control}
                  render={({ field, fieldState }) => (
                    <Field data-invalid={fieldState.invalid}>
                      <FieldLabel htmlFor="site-certificate" required>
                        {t("deployment.address.certificate")}
                      </FieldLabel>
                      <Textarea {...field} id="site-certificate" className="font-mono text-xs md:text-xs" rows={6} spellCheck={false} aria-invalid={fieldState.invalid} />
                      <FieldDescription>{t("deployment.address.certificateHelp")}</FieldDescription>
                    </Field>
                  )}
                />
                <Controller
                  name="privateKey"
                  control={form.control}
                  render={({ field, fieldState }) => (
                    <Field data-invalid={fieldState.invalid}>
                      <FieldLabel htmlFor="site-private-key" required>
                        {t("deployment.address.privateKey")}
                      </FieldLabel>
                      <Textarea
                        {...field}
                        id="site-private-key"
                        className="font-mono text-xs md:text-xs"
                        rows={6}
                        spellCheck={false}
                        autoComplete="off"
                        aria-invalid={fieldState.invalid}
                      />
                    </Field>
                  )}
                />
              </>
            ) : null}
            {status.expiresAt || status.renewalError ? (
              <Field>
                <FieldLabel>{t("deployment.address.status")}</FieldLabel>
                {status.expiresAt ? (
                  <p className="text-sm">
                    {t("deployment.address.expiresAt", { domains: (status.domains ?? []).join(", "), time: formatDateTime(status.expiresAt) })}
                  </p>
                ) : null}
                {status.renewalError && status.renewalFailedAt ? (
                  <p className="text-destructive text-sm">
                    {t("deployment.address.renewalFailed", { time: formatDateTime(status.renewalFailedAt), reason: status.renewalError })}
                  </p>
                ) : null}
              </Field>
            ) : null}
          </>
        ) : null}
        {licensed ? null : <p className="text-sm text-muted-foreground">{t("deployment.branding.locked")}</p>}
        {locales.map((locale) => (
          <FormInputField
            key={locale}
            name={`names.${locale}`}
            control={form.control}
            label={t("deployment.branding.name", { locale })}
            description={t("deployment.branding.nameHelp", { name: __BUILD_BRAND__.names?.[locale] ?? "" })}
            required={false}
            disabled={!licensed}
          />
        ))}
        <FormInputField
          name="sdkName"
          control={form.control}
          label={t("deployment.branding.sdkName")}
          description={t("deployment.branding.sdkNameHelp", { name: __BUILD_BRAND__.sdkName })}
          required={false}
          disabled={!licensed}
        />
        <Controller
          name="icon"
          control={form.control}
          render={({ field }) => <BrandIconField value={field.value} onChange={field.onChange} disabled={!licensed} />}
        />
      </FieldGroup>

      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 网站图标字段：预览当前 PNG 图标，选择不超过 512 KB 的 PNG 替换，或移除后沿用构建图标。 */
function BrandIconField({ value, onChange, disabled }: { value: string; onChange: (value: string) => void; disabled?: boolean }) {
  const { t } = useTranslation("platform")
  const inputRef = useRef<HTMLInputElement>(null)
  const [error, setError] = useState("")

  /** 读取选择的 PNG 文件并以 Base64 写入表单，不是 PNG 或超过大小上限时在字段下方提示。 */
  function pick(file: File | undefined) {
    if (!file || disabled) return
    if (file.type !== "image/png" || file.size > brandIconMaxBytes) {
      setError(t("deployment.branding.iconInvalid"))
      return
    }
    setError("")
    const reader = new FileReader()
    reader.onload = () => onChange(String(reader.result).replace(/^data:[^,]*,/, ""))
    reader.readAsDataURL(file)
  }

  return (
    <Field>
      <FieldLabel htmlFor="brand-icon">{t("deployment.branding.icon")}</FieldLabel>
      <div className="flex items-center gap-4">
        <IconPreview value={value} />
        <Button type="button" variant="outline" disabled={disabled} onClick={() => inputRef.current?.click()}>
          {t(value ? "deployment.branding.replaceIcon" : "deployment.branding.chooseIcon")}
        </Button>
        {value && !disabled ? (
          <Button type="button" variant="ghost" onClick={() => onChange("")}>
            {t("deployment.branding.removeIcon")}
          </Button>
        ) : null}
        <input
          ref={inputRef}
          id="brand-icon"
          type="file"
          accept="image/png"
          className="sr-only"
          disabled={disabled}
          onChange={(event) => {
            pick(event.target.files?.[0])
            event.target.value = ""
          }}
        />
      </div>
      <FieldDescription className={error ? "text-destructive" : undefined}>{error || t("deployment.branding.iconHelp")}</FieldDescription>
    </Field>
  )
}

/** 展示 Base64 PNG 图标的预览，没有图标时显示占位框。 */
function IconPreview({ value }: { value: string }) {
  if (!value) return <div className="size-10 rounded-md border border-dashed" />
  return <img src={`data:image/png;base64,${value}`} alt="" className="size-10 rounded-md border object-contain" />
}
