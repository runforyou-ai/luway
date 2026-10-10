/** 工作区与账号的注册与创建页签：设置账号注册方式和可以创建工作区的账号范围。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import {
  RegistrationPolicy,
  WorkspaceCreationPolicy,
  getLicense,
  getPlatformSettings,
  updatePlatformSettings,
  type PlatformSettings,
} from "@/api"
import { PageContent } from "@/components/page-content"
import { ResourceContent } from "@/components/resource-content"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import {
  registrationSettingsSchema,
  type RegistrationSettingsFormValues,
} from "@/features/platform/platform-registration-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 每次进入页面读取最新策略后渲染自动保存的设置表单；策略不缓存，表单只以本次读取结果为初始值。 */
export function PlatformRegistrationTab() {
  const { t } = useTranslation("platform")
  const settings = useResource(resourceKeys.platformSettings(), (signal) => getPlatformSettings(signal), {
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  const license = useResource(resourceKeys.license(), (signal) => getLicense(signal))

  return (
    <PageContent variant="form">
      <ResourceContent resources={settings} errorMessage={t("registration.loadError")}>
        {settings.data ? (
          <RegistrationSettingsForm settings={settings.data} workspaceLimit={license.data?.capabilities.workspaceLimit} />
        ) : null}
      </ResourceContent>
    </PageContent>
  )
}

/** 把平台级策略转换为表单值。 */
function settingsValues(settings: PlatformSettings): RegistrationSettingsFormValues {
  return {
    registrationPolicy: settings.registrationPolicy as RegistrationSettingsFormValues["registrationPolicy"],
    workspaceCreationPolicy: settings.workspaceCreationPolicy as RegistrationSettingsFormValues["workspaceCreationPolicy"],
  }
}

/** 修改注册策略与工作区创建策略，选择后立即保存；平台有工作区上限时在创建策略下方说明。 */
function RegistrationSettingsForm({
  settings,
  workspaceLimit,
}: {
  settings: PlatformSettings
  workspaceLimit: number | undefined
}) {
  const { t } = useTranslation("platform")
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
      const saved = await updatePlatformSettings(values)
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
                <option value={WorkspaceCreationPolicy.PlatformAdmin}>
                  {t("registration.workspaceCreationPolicies.platformAdmin")}
                </option>
                <option value={WorkspaceCreationPolicy.AnyAccount}>
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
