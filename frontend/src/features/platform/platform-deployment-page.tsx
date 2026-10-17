/** 系统组集成设置页：文件存储、邮件发送与微信开放平台，保存后所有服务器在下次心跳时生效。 */
import { EmailSettingsForm } from "./platform-email-form"
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Navigate, useParams } from "react-router"
import { toast } from "sonner"

import { getPlatformDeployment, updatePlatformStorage, type PlatformDeployment } from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { SwitchField } from "@/components/form/switch-field"
import { PageContent } from "@/components/page-content"
import { ResourceContent } from "@/components/resource-content"
import { FieldGroup } from "@/components/ui/field"
import { AdminPage } from "@/features/admin/admin-page"
import { adminSystemPath, adminSystemSections } from "@/features/admin/admin-paths"
import { createStorageSchema, type StorageFormValues } from "@/features/platform/platform-deployment-schema"
import { PlatformWechatSettings } from "@/features/platform/platform-wechat-settings"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 每次进入页面读取最新部署配置后渲染路由 :section 指定的集成设置页；无效页面回到站点设置。同一组内切换页面不重新读取，各页面表单保持挂载。 */
export function PlatformDeploymentPage() {
  const { t } = useTranslation("platform")
  const params = useParams()
  const section = adminSystemSections.find((value) => value === params.section)
  const deployment = useResource(resourceKeys.platformDeployment(), (signal) => getPlatformDeployment(signal), {
    gcTime: 0,
    refetchOnWindowFocus: false,
  })

  if (!section) return <Navigate to={adminSystemPath} replace />
  const title = t(`deployment.tabs.${section}`)
  if (section === "wechat") {
    return (
      <AdminPage title={title} description={t("deployment.description")}>
        <PageContent variant="form">
          <PlatformWechatSettings />
        </PageContent>
      </AdminPage>
    )
  }
  if (!deployment.data) {
    return (
      <AdminPage title={title} description={t("deployment.description")}>
        <PageContent variant="form">
          <ResourceContent resources={deployment} errorMessage={t("deployment.loadError")}>
            {null}
          </ResourceContent>
        </PageContent>
      </AdminPage>
    )
  }
  return (
    <AdminPage title={title} description={t("deployment.description")}>
      <PageContent variant="form">
        <div className={section === "storage" ? undefined : "hidden"}>
          <StorageForm deployment={deployment.data} />
        </div>
        <div className={section === "email" ? undefined : "hidden"}>
          <EmailForm deployment={deployment.data} />
        </div>
      </PageContent>
    </AdminPage>
  )
}

/** 返回保存部署配置后刷新部署配置的函数。 */
function useDeploymentSaved() {
  const invalidate = useResourceInvalidator()
  return () => {
    void invalidate(resourceKeys.platformDeployment())
  }
}

/** 编辑 S3 接口与文件访问地址，保存前验证实际读写和匿名访问。 */
function StorageForm({ deployment }: { deployment: PlatformDeployment }) {
  const { t } = useTranslation("platform")
  const saved = useDeploymentSaved()
  const schema = useMemo(() => createStorageSchema(t), [t])
  const form = useForm<StorageFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: deployment.storage,
  })
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: updatePlatformStorage,
    onSubmitted: (result) => {
      toast.success(t("deployment.saved"))
      if (result.originAccess === "public") toast.warning(t("deployment.storage.publicWarning"), { duration: 12000 })
      if (result.originAccess === "unknown") toast.warning(t("deployment.storage.unknownWarning"), { duration: 12000 })
      saved()
    },
    errorMessage: t("deployment.saveError"),
    errorFields: ["endpoint", "publicBaseURL", "region", "bucket", "accessKeyID", "secretAccessKey"],
    logLabel: "保存对象存储配置",
  })

  return (
    <form className="space-y-9" aria-label={t("deployment.tabs.storage")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <FormInputField name="endpoint" control={form.control} label={t("deployment.storage.endpoint")} description={t("deployment.storage.endpointHelp")} type="url" required />
        <FormInputField
          name="publicBaseURL"
          required
          control={form.control}
          label={t("deployment.storage.publicBaseURL")}
          description={t("deployment.storage.publicBaseURLHelp")}
          type="url"
        />
        <div className="grid gap-6 sm:grid-cols-2">
          <FormInputField name="region" required control={form.control} label={t("deployment.storage.region")} />
          <FormInputField name="bucket" required control={form.control} label={t("deployment.storage.bucket")} />
        </div>
        <FormInputField name="accessKeyID" required control={form.control} label={t("deployment.storage.accessKeyID")} autoComplete="off" />
        <FormInputField
          name="secretAccessKey"
          required
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
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} />
    </form>
  )
}

/** 编辑系统邮件的接入方式与发件人。 */
function EmailForm({ deployment }: { deployment: PlatformDeployment }) {
  const saved = useDeploymentSaved()
  return <EmailSettingsForm email={deployment.email} onSaved={saved} />
}
