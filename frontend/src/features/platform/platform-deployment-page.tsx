/** 平台设置的部署配置页：以页签分别维护整个部署共用的部署名称、平台时区与上报开关、部署地址与证书、文件存储、邮件发送、部署品牌、提供价格区块时的产品首页和微信开放平台，保存后所有服务器在下次心跳时生效。 */
import { useEffect, useMemo, useRef, useState } from "react"
import { CircleHelpIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  getPlatformDeployment,
  loadInstallationStatus,
  PlatformCertificateSource,
  updatePlatformAddress,
  updatePlatformBranding,
  updatePlatformDeploymentBasics,
  updatePlatformEmail,
  updatePlatformHome,
  updatePlatformStorage,
  type PlatformDeployment,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { SwitchField } from "@/components/form/switch-field"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ProductDocSheet } from "@/components/product-doc-sheet"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import {
  createAddressSchema,
  basicsSchema,
  createBrandingSchema,
  createEmailSchema,
  createHomeSchema,
  createStorageSchema,
  isHTTPSAddress,
  type AddressFormValues,
  type BasicsFormValues,
  type BrandingFormValues,
  type EmailFormValues,
  type HomeFormValues,
  type StorageFormValues,
} from "@/features/platform/platform-deployment-schema"
import { PlatformTabsActions, PlatformTabsPage } from "@/features/platform/platform-tabs"
import { PlatformWechatSettings } from "@/features/platform/platform-wechat-settings"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { applyBrand } from "@/lib/brand"
import type { ProductDocsPage } from "@/lib/product-docs"
import { supportedTimeZones } from "@/lib/time-zones"
import { zodResolver } from "@/lib/zod-resolver"

/** 部署配置页的页签；产品首页页签只在首页提供价格区块时显示。 */
const deploymentTabs = ["basics", "address", "storage", "email", "branding", "home", "wechat"] as const

/** 部署配置页的页签名称。 */
type DeploymentTab = (typeof deploymentTabs)[number]

/** 各页签在侧栏打开的配置文档。 */
const tabDocs: Partial<Record<DeploymentTab, ProductDocsPage>> = {
  basics: "deploymentSettings",
  address: "deploymentHTTPS",
  storage: "deploymentStorage",
  email: "deploymentEmail",
  branding: "deploymentBranding",
  wechat: "wechatOpenPlatform",
}

/** 网站图标的最大字节数，与服务端校验一致。 */
const brandIconMaxBytes = 512 * 1024

/** 每次进入页面读取最新部署配置后按与地址同步的页签渲染，读取期间与失败时显示读取状态；产品首页页签只在首页提供价格区块时显示。 */
export function PlatformDeploymentPage() {
  const { t } = useTranslation("platform")
  const deployment = useResource(resourceKeys.platformDeployment(), (signal) => getPlatformDeployment(signal), {
    gcTime: 0,
    refetchOnWindowFocus: false,
  })

  if (!deployment.data)
    return (
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <PageHeader title={t("deployment.title")} description={t("deployment.description")} />
        <PageContent variant="form">
          <ResourceContent resources={deployment} errorMessage={t("deployment.loadError")}>
            {null}
          </ResourceContent>
        </PageContent>
      </div>
    )
  const data = deployment.data
  return (
    <PlatformTabsPage
      title={t("deployment.title")}
      description={t("deployment.description")}
      tabs={deploymentTabs
        .filter((value) => value !== "home" || data.homePricing)
        .map((value) => ({ value, label: t(`deployment.tabs.${value}`) }))}
    >
      {(tab) => <DeploymentTabContent tab={tab} deployment={data} />}
    </PlatformTabsPage>
  )
}

/** 渲染各页签表单，并在侧栏打开当前页签的配置文档；各页签表单常驻，切换页签保留未保存的修改，微信开放平台页签首次选中后读取并保持挂载。 */
function DeploymentTabContent({ tab, deployment }: { tab: DeploymentTab; deployment: PlatformDeployment }) {
  const { t } = useTranslation(["platform", "common"])
  const [docsTrigger, setDocsTrigger] = useState<HTMLElement | null>(null)
  const [wechatOpened, setWechatOpened] = useState(tab === "wechat")

  // 微信开放平台页签首次选中后保持挂载，切换页签保留未保存的凭据。
  useEffect(() => {
    if (tab === "wechat") setWechatOpened(true)
  }, [tab])

  return (
    <>
      <PlatformTabsActions>
        {tabDocs[tab] ? (
          <Button type="button" variant="ghost" size="sm" onClick={(event) => setDocsTrigger(event.currentTarget)}>
            <CircleHelpIcon />
            {t("common:productDocs")}
          </Button>
        ) : null}
      </PlatformTabsActions>
      <PageContent variant="form">
        <div className={tab === "wechat" ? "hidden" : undefined}>
          <div className={tab === "basics" ? undefined : "hidden"}>
            <BasicsForm deployment={deployment} />
          </div>
          <div className={tab === "address" ? undefined : "hidden"}>
            <AddressForm deployment={deployment} />
          </div>
          <div className={tab === "storage" ? undefined : "hidden"}>
            <StorageForm deployment={deployment} />
          </div>
          <div className={tab === "email" ? undefined : "hidden"}>
            <EmailForm deployment={deployment} />
          </div>
          <div className={tab === "branding" ? undefined : "hidden"}>
            <BrandingForm deployment={deployment} />
          </div>
          {deployment.homePricing ? (
            <div className={tab === "home" ? undefined : "hidden"}>
              <HomeForm deployment={deployment} />
            </div>
          ) : null}
        </div>
        {wechatOpened || tab === "wechat" ? (
          <div className={tab === "wechat" ? undefined : "hidden"}>
            <PlatformWechatSettings />
          </div>
        ) : null}
      </PageContent>
      <ProductDocSheet page={docsTrigger ? (tabDocs[tab] ?? null) : null} trigger={docsTrigger} onClose={() => setDocsTrigger(null)} />
    </>
  )
}

/** 返回保存部署配置后刷新部署配置与安装状态的函数。 */
function useDeploymentSaved() {
  const invalidate = useResourceInvalidator()
  return () => {
    void invalidate(resourceKeys.platformDeployment())
    void invalidate(resourceKeys.installationStatus())
  }
}

/** 编辑部署名称、平台时区与上报开关；时区变化时服务端按新时区重建运营数据，保存后刷新概览。 */
function BasicsForm({ deployment }: { deployment: PlatformDeployment }) {
  const { t } = useTranslation("platform")
  const saved = useDeploymentSaved()
  const invalidate = useResourceInvalidator()
  const timeZones = useMemo(() => supportedTimeZones(deployment.timeZone), [deployment.timeZone])
  const form = useForm<BasicsFormValues>({
    resolver: zodResolver(basicsSchema),
    shouldUseNativeValidation: true,
    defaultValues: {
      name: deployment.name,
      timeZone: deployment.timeZone,
      telemetryEnabled: deployment.telemetryEnabled,
    },
  })
  const { submit } = useFormSave({
    form,
    schema: basicsSchema,
    autoSave: false,
    save: updatePlatformDeploymentBasics,
    onSubmitted: () => {
      toast.success(t("deployment.saved"))
      saved()
      void invalidate(resourceKeys.platformOverview())
    },
    errorMessage: t("deployment.saveError"),
    errorFields: ["name", "timeZone"],
    logLabel: "保存部署基本配置",
  })

  return (
    <form className="space-y-9" aria-label={t("deployment.tabs.basics")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <FormInputField name="name" control={form.control} label={t("deployment.name")} description={t("deployment.nameHelp")} required={false} />
        <Controller
          name="timeZone"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="deployment-time-zone" required>
                {t("deployment.timeZone")}
              </FieldLabel>
              <NativeSelect {...field} id="deployment-time-zone" aria-invalid={fieldState.invalid}>
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
          name="telemetryEnabled"
          control={form.control}
          render={({ field }) => (
            <SwitchField
              id="deployment-telemetry"
              name={field.name}
              label={t("deployment.telemetry")}
              description={t("deployment.telemetryHelp")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 编辑部署地址与证书：部署中有直接提供 HTTPS 的服务器且地址为 HTTPS 时选择自动签发或上传证书，并展示证书的域名、到期时间与最近一次自动签发失败；自动签发时保存前由服务端签发证书。 */
function AddressForm({ deployment }: { deployment: PlatformDeployment }) {
  const { t } = useTranslation("platform")
  const saved = useDeploymentSaved()
  const { formatDateTime } = useDateTime()
  const schema = useMemo(() => createAddressSchema(t, deployment.httpsServers), [t, deployment.httpsServers])
  const form = useForm<AddressFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      publicURL: deployment.publicURL,
      source: deployment.certificate.source,
      certificate: deployment.certificate.certificate,
      privateKey: deployment.certificate.privateKey,
    },
  })
  const https = isHTTPSAddress(form.watch("publicURL"))
  const source = form.watch("source")
  const status = deployment.certificateStatus
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: (values) =>
      updatePlatformAddress({
        publicURL: values.publicURL,
        certificate: { source: values.source, certificate: values.certificate, privateKey: values.privateKey },
      }),
    onSubmitted: () => {
      toast.success(t("deployment.saved"))
      saved()
    },
    errorMessage: t("deployment.saveError"),
    errorFields: ["publicURL", "certificateSource", "certificate", "privateKey"],
    logLabel: "保存部署地址",
  })

  return (
    <form className="space-y-9" aria-label={t("deployment.tabs.address")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <FormInputField
          name="publicURL"
          control={form.control}
          label={t("deployment.publicURL")}
          description={t(https && !deployment.httpsServers ? "deployment.address.proxyHelp" : "deployment.publicURLHelp")}
          type="url"
        />
        {https && deployment.httpsServers ? (
          <>
            <Controller
              name="source"
              control={form.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <FieldLabel htmlFor="deployment-certificate-source" required>
                    {t("deployment.address.source")}
                  </FieldLabel>
                  <NativeSelect {...field} id="deployment-certificate-source" aria-invalid={fieldState.invalid}>
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
                      <FieldLabel htmlFor="deployment-certificate" required>
                        {t("deployment.address.certificate")}
                      </FieldLabel>
                      <Textarea {...field} id="deployment-certificate" className="font-mono text-xs md:text-xs" rows={6} spellCheck={false} aria-invalid={fieldState.invalid} />
                      <FieldDescription>{t("deployment.address.certificateHelp")}</FieldDescription>
                    </Field>
                  )}
                />
                <Controller
                  name="privateKey"
                  control={form.control}
                  render={({ field, fieldState }) => (
                    <Field data-invalid={fieldState.invalid}>
                      <FieldLabel htmlFor="deployment-private-key" required>
                        {t("deployment.address.privateKey")}
                      </FieldLabel>
                      <Textarea {...field} id="deployment-private-key" className="font-mono text-xs md:text-xs" rows={6} spellCheck={false} autoComplete="off" aria-invalid={fieldState.invalid} />
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
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 编辑对象存储配置：关闭时隐藏连接字段，开启后保存前由服务端确认能访问存储桶。 */
function StorageForm({ deployment }: { deployment: PlatformDeployment }) {
  const { t } = useTranslation("platform")
  const saved = useDeploymentSaved()
  const schema = useMemo(() => createStorageSchema(t), [t])
  const form = useForm<StorageFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: deployment.storage,
  })
  const enabled = form.watch("enabled")
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: updatePlatformStorage,
    onSubmitted: () => {
      toast.success(t("deployment.saved"))
      saved()
    },
    errorMessage: t("deployment.saveError"),
    errorFields: ["endpoint", "publicBaseURL", "region", "bucket", "accessKeyID", "secretAccessKey"],
    logLabel: "保存对象存储配置",
  })

  return (
    <form className="space-y-9" aria-label={t("deployment.tabs.storage")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <Controller
          name="enabled"
          control={form.control}
          render={({ field }) => (
            <SwitchField
              id="storage-enabled"
              name={field.name}
              label={t("deployment.storage.enabled")}
              description={t(enabled ? "deployment.storage.enabledHelp" : "deployment.storage.disabledHelp")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
        {enabled ? (
          <>
            <FormInputField name="endpoint" control={form.control} label={t("deployment.storage.endpoint")} type="url" />
            <FormInputField
              name="publicBaseURL"
              control={form.control}
              label={t("deployment.storage.publicBaseURL")}
              description={t("deployment.storage.publicBaseURLHelp")}
              type="url"
            />
            <div className="grid gap-6 sm:grid-cols-2">
              <FormInputField name="region" control={form.control} label={t("deployment.storage.region")} />
              <FormInputField name="bucket" control={form.control} label={t("deployment.storage.bucket")} />
            </div>
            <FormInputField name="accessKeyID" control={form.control} label={t("deployment.storage.accessKeyID")} autoComplete="off" />
            <FormInputField
              name="secretAccessKey"
              control={form.control}
              label={t("deployment.storage.secretAccessKey")}
              autoComplete="new-password"
              passwordVisibilityLabels={{ show: t("deployment.showSecret"), hide: t("deployment.hideSecret") }}
            />
            <Controller
              name="forcePathStyle"
              control={form.control}
              render={({ field }) => (
                <SwitchField
                  id="storage-force-path-style"
                  name={field.name}
                  label={t("deployment.storage.forcePathStyle")}
                  description={t("deployment.storage.forcePathStyleHelp")}
                  checked={field.value}
                  onBlur={field.onBlur}
                  onCheckedChange={field.onChange}
                  ref={field.ref}
                />
              )}
            />
          </>
        ) : null}
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 编辑 SMTP 邮件发送配置，主机留空时关闭邮件发送并隐藏其余字段。 */
function EmailForm({ deployment }: { deployment: PlatformDeployment }) {
  const { t } = useTranslation("platform")
  const saved = useDeploymentSaved()
  const schema = useMemo(() => createEmailSchema(t), [t])
  const form = useForm<EmailFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { ...deployment.email, port: String(deployment.email.port) },
  })
  const enabled = form.watch("host").trim() !== ""
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: (values) => updatePlatformEmail({ ...values, port: Number(values.port) }),
    onSubmitted: () => {
      toast.success(t("deployment.saved"))
      saved()
    },
    errorMessage: t("deployment.saveError"),
    errorFields: ["host", "port", "security", "username", "password", "fromAddress"],
    logLabel: "保存邮件发送配置",
  })

  return (
    <form className="space-y-9" aria-label={t("deployment.tabs.email")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <FormInputField name="host" control={form.control} label={t("deployment.email.host")} description={t("deployment.email.hostHelp")} required={false} />
        {enabled ? (
          <>
            <div className="grid gap-6 sm:grid-cols-2">
              <FormInputField name="port" control={form.control} label={t("deployment.email.port")} inputMode="numeric" />
              <Controller
                name="security"
                control={form.control}
                render={({ field, fieldState }) => (
                  <Field data-invalid={fieldState.invalid}>
                    <FieldLabel htmlFor={field.name} required>
                      {t("deployment.email.security")}
                    </FieldLabel>
                    <NativeSelect {...field} id={field.name} aria-invalid={fieldState.invalid}>
                      <option value="starttls">STARTTLS</option>
                      <option value="tls">SSL/TLS</option>
                      <option value="none">{t("deployment.email.securityNone")}</option>
                    </NativeSelect>
                  </Field>
                )}
              />
            </div>
            <FormInputField name="fromAddress" control={form.control} label={t("deployment.email.fromAddress")} type="email" />
            <div className="grid gap-6 sm:grid-cols-2">
              <FormInputField name="username" control={form.control} label={t("deployment.email.username")} autoComplete="off" required={false} deps={["password"]} />
              <FormInputField
                name="password"
                control={form.control}
                label={t("deployment.email.password")}
                autoComplete="new-password"
                required={false}
                passwordVisibilityLabels={{ show: t("deployment.showSecret"), hide: t("deployment.hideSecret") }}
              />
            </div>
          </>
        ) : null}
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 编辑部署品牌：各界面语言的产品名称、网站嵌入脚本对象名与网站图标；授权未授予自定义品牌时说明保存后暂不生效，保存后刷新当前页面的品牌。 */
function BrandingForm({ deployment }: { deployment: PlatformDeployment }) {
  const { t } = useTranslation("platform")
  const saved = useDeploymentSaved()
  const locales = useMemo(() => Object.keys(__BUILD_BRAND__.names ?? {}).sort(), [])
  const schema = useMemo(() => createBrandingSchema(t), [t])
  const form = useForm<BrandingFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      names: Object.fromEntries(locales.map((locale) => [locale, deployment.branding.names[locale] ?? ""])),
      sdkName: deployment.branding.sdkName,
      icon: deployment.branding.icon,
    },
  })
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: updatePlatformBranding,
    onSubmitted: () => {
      toast.success(t("deployment.saved"))
      saved()
      void loadInstallationStatus().then(
        (installation) => applyBrand(installation.brand),
        (error: unknown) => console.warn("同步品牌失败", error),
      )
    },
    errorMessage: t("deployment.saveError"),
    errorFields: ["names", "sdkName", "icon"],
    logLabel: "保存部署品牌",
  })

  return (
    <form className="space-y-9" aria-label={t("deployment.tabs.branding")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        {deployment.brandingLicensed ? null : (
          <p className="text-sm text-muted-foreground">{t("deployment.branding.unlicensed")}</p>
        )}
        {locales.map((locale) => (
          <FormInputField
            key={locale}
            name={`names.${locale}`}
            control={form.control}
            label={t("deployment.branding.name", { locale })}
            description={t("deployment.branding.nameHelp", { name: __BUILD_BRAND__.names?.[locale] ?? "" })}
            required={false}
          />
        ))}
        <FormInputField
          name="sdkName"
          control={form.control}
          label={t("deployment.branding.sdkName")}
          description={t("deployment.branding.sdkNameHelp", { name: __BUILD_BRAND__.sdkName })}
          required={false}
        />
        <Controller
          name="icon"
          control={form.control}
          render={({ field }) => (
            <BrandIconField value={field.value} onChange={field.onChange} />
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 编辑产品首页配置：首页是否展示自部署介绍。 */
function HomeForm({ deployment }: { deployment: PlatformDeployment }) {
  const { t } = useTranslation("platform")
  const saved = useDeploymentSaved()
  const schema = useMemo(() => createHomeSchema(), [])
  const form = useForm<HomeFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: deployment.home,
  })
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: updatePlatformHome,
    onSubmitted: () => {
      toast.success(t("deployment.saved"))
      saved()
    },
    errorMessage: t("deployment.saveError"),
    logLabel: "保存产品首页配置",
  })

  return (
    <form className="space-y-9" aria-label={t("deployment.tabs.home")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <Controller
          name="selfHost"
          control={form.control}
          render={({ field }) => (
            <SwitchField
              id="home-self-host"
              name={field.name}
              label={t("deployment.home.selfHost")}
              description={t("deployment.home.selfHostHelp")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 网站图标字段：预览当前 PNG 图标，选择不超过 512 KB 的 PNG 替换，或移除后沿用构建图标。 */
function BrandIconField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { t } = useTranslation("platform")
  const inputRef = useRef<HTMLInputElement>(null)
  const [error, setError] = useState("")

  /** 读取选择的 PNG 文件并以 Base64 写入表单，不是 PNG 或超过大小上限时在字段下方提示。 */
  function pick(file: File | undefined) {
    if (!file) return
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
        <Button type="button" variant="outline" onClick={() => inputRef.current?.click()}>
          {t(value ? "deployment.branding.replaceIcon" : "deployment.branding.chooseIcon")}
        </Button>
        {value ? (
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
