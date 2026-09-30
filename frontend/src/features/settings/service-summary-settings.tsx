/** 企业会话小结设置：判断模型、小结模型与小结语言，修改后自动保存。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"
import { z } from "zod"

import {
  AIModelType,
  getServiceSummarySettings,
  listAgentModelOptions,
  listAIProviders,
  Locale,
  updateServiceSummarySettings,
} from "@/api"
import { ResourceContent } from "@/components/resource-content"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"
import { ModelGroupOptions, groupChatModels, modelReference, modelValue, type ModelGroup } from "./model-options"

const serviceSummarySchema = z.object({
  decision: z.string(),
  summary: z.string(),
  locale: z.enum([Locale.LocaleChineseSimplified, Locale.LocaleEnglishUnitedStates]),
})

type ServiceSummaryFormValues = z.infer<typeof serviceSummarySchema>

/** 读取会话小结设置与可选模型并显示设置表单。 */
export function ServiceSummarySettings() {
  const { t } = useTranslation("settings")
  const settings = useResource(resourceKeys.serviceSummarySettings(), () => getServiceSummarySettings())
  const providers = useResource(resourceKeys.aiProviders(), () => listAIProviders(), { staleTime: 0 })
  const chatModels = useResource(resourceKeys.agentModelOptions(), () => listAgentModelOptions(), { staleTime: 0 })
  // 判断模型取判断用途的模型，小结模型取支持文本输入的对话模型。
  const decisionGroups: ModelGroup[] = (providers.data?.providers ?? [])
    .map((provider) => ({
      id: provider.id,
      name: provider.name,
      models: provider.models.filter((model) => model.type === AIModelType.AIModelTypeDecision),
    }))
    .filter((provider) => provider.models.length > 0)
  const summaryGroups = groupChatModels(chatModels.data ?? [])
  return (
    <ResourceContent resources={[settings, providers, chatModels]} errorMessage={t("customerService.summary.loadError")}>
      {settings.data && providers.data && chatModels.data ? (
        <ServiceSummaryForm
          groups={{ decision: decisionGroups, summary: summaryGroups }}
          values={{
            decision: modelValue(settings.data.decision),
            summary: modelValue(settings.data.summary),
            locale: settings.data.locale as ServiceSummaryFormValues["locale"],
          }}
        />
      ) : null}
    </ResourceContent>
  )
}

/** 维护判断模型、小结模型与小结语言，修改后自动保存。 */
function ServiceSummaryForm({
  groups,
  values,
}: {
  groups: Record<"decision" | "summary", ModelGroup[]>
  values: ServiceSummaryFormValues
}) {
  const { t } = useTranslation(["settings", "common"])
  const invalidate = useResourceInvalidator()
  const form = useForm<ServiceSummaryFormValues>({
    resolver: zodResolver(serviceSummarySchema),
    shouldUseNativeValidation: true,
    mode: "onChange",
    defaultValues: values,
  })
  const { submit } = useFormSave({
    form,
    schema: serviceSummarySchema,
    autoSave: true,
    save: async (values) => {
      await updateServiceSummarySettings({
        decision: modelReference(values.decision),
        summary: modelReference(values.summary),
        locale: values.locale,
      })
      void invalidate(resourceKeys.serviceSummarySettings())
    },
    errorMessage: t("customerService.summary.saveError"),
    errorFields: ["decision", "summary", "locale"],
    logLabel: "保存会话小结设置",
  })

  return (
    <form
      className="w-full"
      aria-label={t("customerService.summary.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        {(["decision", "summary"] as const).map((name) => (
            <Controller
              key={name}
              name={name}
              control={form.control}
              render={({ field }) => (
                <Field>
                  <FieldLabel htmlFor={`summary-${name}`}>
                    {t(`customerService.summary.${name}`)}
                  </FieldLabel>
                  <NativeSelect {...field} id={`summary-${name}`}>
                    <ModelGroupOptions groups={groups[name]} />
                  </NativeSelect>
                  <FieldDescription>
                    {t(`customerService.summary.${name}Description`)}
                    {groups[name].length === 0 ? (
                      <>
                        {" "}
                        <Link to="/settings/model-services">
                          {t("customerService.models.configureModels")}
                        </Link>
                      </>
                    ) : null}
                  </FieldDescription>
                </Field>
              )}
            />
        ))}
        <Controller
          name="locale"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="summary-locale" required>
                {t("customerService.summary.locale")}
              </FieldLabel>
              <NativeSelect {...field} id="summary-locale" required>
                <option value={Locale.LocaleChineseSimplified}>
                  {t("preferences.languages.zhCN")}
                </option>
                <option value={Locale.LocaleEnglishUnitedStates}>
                  {t("preferences.languages.enUS")}
                </option>
              </NativeSelect>
              <FieldDescription>{t("customerService.summary.localeDescription")}</FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}
