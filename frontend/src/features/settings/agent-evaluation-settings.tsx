/** 工作区评测设置：评测使用的判断模型，修改后自动保存。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"
import { z } from "zod"

import { AIModelUsage, getAgentEvaluationSettings, updateAgentEvaluationSettings, type AIModelOption } from "@/api"
import { AIModelOptionGroups, useAIModelOptions } from "@/components/ai-model-options"
import { ResourceContent } from "@/components/resource-content"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 评测设置表单校验规则。 */
const evaluationSettingsSchema = z.object({ decisionModelId: z.string() })

type EvaluationSettingsFormValues = z.infer<typeof evaluationSettingsSchema>

/** 读取评测设置与可选对话模型并显示设置表单。 */
export function AgentEvaluationSettings() {
  const { t } = useTranslation("settings")
  const settings = useResource(resourceKeys.agentEvaluationSettings(), () => getAgentEvaluationSettings())
  const models = useAIModelOptions(AIModelUsage.Decision)
  return (
    <ResourceContent resources={[settings, models]} errorMessage={t("aiEvaluation.loadError")}>
      {settings.data && models.data ? (
        <EvaluationSettingsForm
          models={models.data}
          values={{ decisionModelId: settings.data.decisionModelId ?? "" }}
        />
      ) : null}
    </ResourceContent>
  )
}

/** 维护评测模型，修改后自动保存。 */
function EvaluationSettingsForm({ models, values }: { models: AIModelOption[]; values: EvaluationSettingsFormValues }) {
  const { t } = useTranslation(["settings", "common"])
  const invalidate = useResourceInvalidator()
  const form = useForm<EvaluationSettingsFormValues>({
    resolver: zodResolver(evaluationSettingsSchema),
    shouldUseNativeValidation: true,
    mode: "onChange",
    defaultValues: values,
  })
  const { submit } = useFormSave({
    form,
    schema: evaluationSettingsSchema,
    autoSave: true,
    // 空值表示关闭评测。
    save: async (values) => {
      await updateAgentEvaluationSettings( { decisionModelId: values.decisionModelId || null })
      void invalidate(resourceKeys.agentEvaluationSettings())
      void invalidate(resourceKeys.agentEvaluation())
    },
    errorMessage: t("aiEvaluation.saveError"),
    errorFields: ["decisionModelId"],
    logLabel: "保存评测设置",
  })

  return (
    <form
      className="w-full"
      aria-label={t("aiEvaluation.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="decisionModelId"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="evaluation-model">{t("aiEvaluation.model")}</FieldLabel>
              <NativeSelect {...field} id="evaluation-model">
                <option value="">{t("customerService.models.notUsed")}</option>
                <AIModelOptionGroups models={models} />
              </NativeSelect>
              <FieldDescription>
                {t("aiEvaluation.modelDescription")}
                {models.length === 0 ? (
                  <>
                    {" "}
                    <Link to="/settings/model-services">{t("customerService.models.configureModels")}</Link>
                  </>
                ) : null}
              </FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}
