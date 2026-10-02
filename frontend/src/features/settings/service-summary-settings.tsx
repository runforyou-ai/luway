/** 企业会话小结设置：判断模型、小结模型与小结语言，修改后自动保存。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"
import { z } from "zod"

import {
  AIModelUsage,
  getServiceSummarySettings,
  Locale,
  updateServiceSummarySettings,
  type AIModelOptionData,
} from "@/api"
import { AIModelOptionGroups, useAIModelOptions } from "@/components/ai-model-options"
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

const serviceSummarySchema = z.object({
  decisionModelId: z.string(),
  summaryModelId: z.string(),
  locale: z.enum([Locale.LocaleChineseSimplified, Locale.LocaleEnglishUnitedStates]),
})

type ServiceSummaryFormValues = z.infer<typeof serviceSummarySchema>

/** 读取会话小结设置与可选模型并显示设置表单。 */
export function ServiceSummarySettings() {
  const { t } = useTranslation("settings")
  const settings = useResource(resourceKeys.serviceSummarySettings(), () => getServiceSummarySettings())
  const decisionModels = useAIModelOptions(AIModelUsage.AIModelUsageDecision)
  const summaryModels = useAIModelOptions(AIModelUsage.AIModelUsageSummary)
  return (
    <ResourceContent resources={[settings, decisionModels, summaryModels]} errorMessage={t("customerService.summary.loadError")}>
      {settings.data && decisionModels.data && summaryModels.data ? (
        <ServiceSummaryForm
          models={{ decisionModelId: decisionModels.data, summaryModelId: summaryModels.data }}
          values={{
            decisionModelId: settings.data.decisionModelId ?? "",
            summaryModelId: settings.data.summaryModelId ?? "",
            locale: settings.data.locale as ServiceSummaryFormValues["locale"],
          }}
        />
      ) : null}
    </ResourceContent>
  )
}

/** 维护判断模型、小结模型与小结语言，修改后自动保存。 */
function ServiceSummaryForm({
  models,
  values,
}: {
  models: Record<"decisionModelId" | "summaryModelId", AIModelOptionData[]>
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
        decisionModelId: values.decisionModelId || null,
        summaryModelId: values.summaryModelId || null,
        locale: values.locale,
      })
      void invalidate(resourceKeys.serviceSummarySettings())
    },
    errorMessage: t("customerService.summary.saveError"),
    errorFields: ["decisionModelId", "summaryModelId", "locale"],
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
        {([
          ["decisionModelId", "decision"],
          ["summaryModelId", "summary"],
        ] as const).map(([name, label]) => (
            <Controller
              key={name}
              name={name}
              control={form.control}
              render={({ field }) => (
                <Field>
                  <FieldLabel htmlFor={`summary-${label}`}>
                    {t(`customerService.summary.${label}`)}
                  </FieldLabel>
                  <NativeSelect {...field} id={`summary-${label}`}>
                    <option value="">{t("customerService.models.notUsed")}</option>
                    <AIModelOptionGroups models={models[name]} />
                  </NativeSelect>
                  <FieldDescription>
                    {t(`customerService.summary.${label}Description`)}
                    {models[name].length === 0 ? (
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
