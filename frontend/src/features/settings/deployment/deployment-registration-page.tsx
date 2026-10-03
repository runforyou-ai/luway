/** 部署设置的注册与创建页：设置账号注册方式和可以创建工作区的账号范围。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import {
  RegistrationPolicy,
  WorkspaceCreationPolicy,
  getDeploymentOverview,
  getDeploymentSettings,
  updateDeploymentSettings,
  type DeploymentSettings,
} from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import {
  registrationSettingsSchema,
  type RegistrationSettingsFormValues,
} from "@/features/settings/deployment/deployment-registration-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 每次进入页面读取最新策略后渲染自动保存的设置表单；策略不缓存，表单只以本次读取结果为初始值。 */
export function DeploymentRegistrationPage() {
  const { t } = useTranslation("deployment")
  const settings = useResource(resourceKeys.deploymentSettings(), (signal) => getDeploymentSettings(signal), {
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  const overview = useResource(resourceKeys.deploymentOverview(), (signal) => getDeploymentOverview(signal))

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("registration.title")} description={t("registration.description")} />
      <PageContent variant="form">
        <ResourceContent resources={settings} errorMessage={t("registration.loadError")}>
          {settings.data ? (
            <RegistrationSettingsForm settings={settings.data} workspaceLimit={overview.data?.capabilities.workspaceLimit} />
          ) : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 把部署级策略转换为表单值。 */
function settingsValues(settings: DeploymentSettings): RegistrationSettingsFormValues {
  return {
    registrationPolicy: settings.registrationPolicy as RegistrationSettingsFormValues["registrationPolicy"],
    workspaceCreationPolicy: settings.workspaceCreationPolicy as RegistrationSettingsFormValues["workspaceCreationPolicy"],
  }
}

/** 修改注册策略与工作区创建策略，选择后立即保存；实例有工作区上限时在创建策略下方说明。 */
function RegistrationSettingsForm({
  settings,
  workspaceLimit,
}: {
  settings: DeploymentSettings
  workspaceLimit: number | undefined
}) {
  const { t } = useTranslation("deployment")
  const invalidate = useResourceInvalidator()
  const form = useForm<RegistrationSettingsFormValues>({
    resolver: zodResolver(registrationSettingsSchema),
    shouldUseNativeValidation: true,
    defaultValues: settingsValues(settings),
  })
  const { submit } = useFormSave({
    form,
    schema: registrationSettingsSchema,
    autoSave: true,
    save: async (values) => {
      const saved = await updateDeploymentSettings(values)
      void invalidate(resourceKeys.installationStatus())
      void invalidate(resourceKeys.workspaces())
      return saved
    },
    savedValues: (saved) => settingsValues(saved),
    errorMessage: t("registration.saveError"),
    errorFields: ["registrationPolicy", "workspaceCreationPolicy"],
    logLabel: "保存注册与创建设置",
  })
  const registrationPolicy = form.watch("registrationPolicy")

  return (
    <form className="w-full" aria-label={t("registration.formLabel")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="grid">
        <Controller
          name="registrationPolicy"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("registration.registrationPolicy")}
              </FieldLabel>
              <NativeSelect {...field} id={field.name} aria-invalid={fieldState.invalid}>
                <option value={RegistrationPolicy.RegistrationPolicyInvitationOnly}>
                  {t("registration.registrationPolicies.invitationOnly")}
                </option>
                <option value={RegistrationPolicy.RegistrationPolicyOpen}>{t("registration.registrationPolicies.open")}</option>
              </NativeSelect>
              <FieldDescription>
                {registrationPolicy === RegistrationPolicy.RegistrationPolicyOpen
                  ? t("registration.registrationHelp.open")
                  : t("registration.registrationHelp.invitationOnly")}
              </FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="workspaceCreationPolicy"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("registration.workspaceCreationPolicy")}
              </FieldLabel>
              <NativeSelect {...field} id={field.name} aria-invalid={fieldState.invalid}>
                <option value={WorkspaceCreationPolicy.WorkspaceCreationPolicyDeploymentAdmin}>
                  {t("registration.workspaceCreationPolicies.deploymentAdmin")}
                </option>
                <option value={WorkspaceCreationPolicy.WorkspaceCreationPolicyAnyAccount}>
                  {t("registration.workspaceCreationPolicies.anyAccount")}
                </option>
              </NativeSelect>
              {workspaceLimit ? (
                <FieldDescription>{t("registration.workspaceLimitHelp", { count: workspaceLimit })}</FieldDescription>
              ) : null}
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}
