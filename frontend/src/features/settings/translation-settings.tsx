/** 企业翻译设置：翻译客户会话消息的模型，修改后自动保存。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"
import { z } from "zod"

import { getTranslationSettings, listAgentModelOptions, updateTranslationSettings } from "@/api"
import { ResourceContent } from "@/components/resource-content"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"
import { ModelGroupOptions, groupChatModels, modelReference, modelValue, type ModelGroup } from "./model-options"

const translationSchema = z.object({ model: z.string() })

type TranslationFormValues = z.infer<typeof translationSchema>

/** 读取翻译设置与可选对话模型并显示设置表单。 */
export function TranslationSettings() {
  const { t } = useTranslation("settings")
  const settings = useResource(resourceKeys.translationSettings(), () => getTranslationSettings())
  const chatModels = useResource(resourceKeys.agentModelOptions(), () => listAgentModelOptions(), { staleTime: 0 })
  // 翻译模型取支持文本输入的对话模型，按供应商分组。
  const groups = groupChatModels(chatModels.data ?? [])
  return (
    <ResourceContent resources={[settings, chatModels]} errorMessage={t("customerService.translation.loadError")}>
      {settings.data && chatModels.data ? (
        <TranslationForm
          groups={groups}
          values={{ model: modelValue(settings.data.model) }}
        />
      ) : null}
    </ResourceContent>
  )
}

/** 维护翻译模型，修改后自动保存。 */
function TranslationForm({ groups, values }: { groups: ModelGroup[]; values: TranslationFormValues }) {
  const { t } = useTranslation(["settings", "common"])
  const invalidate = useResourceInvalidator()
  const form = useForm<TranslationFormValues>({
    resolver: zodResolver(translationSchema),
    shouldUseNativeValidation: true,
    mode: "onChange",
    defaultValues: values,
  })
  const { submit } = useFormSave({
    form,
    schema: translationSchema,
    autoSave: true,
    // 空值表示关闭翻译。
    save: async (values) => {
      await updateTranslationSettings({ model: modelReference(values.model) })
      void invalidate(resourceKeys.translationSettings())
      void invalidate(resourceKeys.conversationTranslation())
    },
    errorMessage: t("customerService.translation.saveError"),
    errorFields: ["model"],
    logLabel: "保存翻译设置",
  })

  return (
    <form
      className="w-full"
      aria-label={t("customerService.translation.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="model"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="translation-model">{t("customerService.translation.model")}</FieldLabel>
              <NativeSelect {...field} id="translation-model">
                <ModelGroupOptions groups={groups} />
              </NativeSelect>
              <FieldDescription>
                {t("customerService.translation.modelDescription")}
                {groups.length === 0 ? (
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
