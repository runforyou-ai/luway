/** 工作区媒体理解设置：主模型不能看图或听语音时的兜底模型。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"
import { z } from "zod"

import {
  AIModelUsage,
  getMediaUnderstandingSettings,
  updateMediaUnderstandingSettings,
  type AIModelOption,
} from "@/api"
import { AIModelOptionGroups, useAIModelOptions } from "@/components/ai-model-options"
import { ResourceContent } from "@/components/resource-content"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

const mediaSchema = z.object({
  ocrModelId: z.string(),
  transcriptionModelId: z.string(),
})

type MediaFormValues = z.infer<typeof mediaSchema>

/** 读取媒体理解设置与可选模型并显示设置表单。 */
export function MediaUnderstandingSettings() {
  const { t } = useTranslation("settings")
  const settings = useResource(resourceKeys.mediaUnderstandingSettings(), () => getMediaUnderstandingSettings())
  const ocrModels = useAIModelOptions(AIModelUsage.OCR)
  const transcriptionModels = useAIModelOptions(AIModelUsage.Transcription)
  return (
    <ResourceContent
      resources={[settings, ocrModels, transcriptionModels]}
      errorMessage={t("mediaUnderstanding.loadError")}
    >
      {settings.data && ocrModels.data && transcriptionModels.data ? (
        <MediaUnderstandingForm
          ocrModels={ocrModels.data}
          transcriptionModels={transcriptionModels.data}
          values={{
            ocrModelId: settings.data.ocrModelId ?? "",
            transcriptionModelId: settings.data.transcriptionModelId ?? "",
          }}
        />
      ) : null}
    </ResourceContent>
  )
}

function MediaUnderstandingForm({
  ocrModels,
  transcriptionModels,
  values,
}: {
  ocrModels: AIModelOption[]
  transcriptionModels: AIModelOption[]
  values: MediaFormValues
}) {
  const { t } = useTranslation(["settings", "common"])
  const invalidate = useResourceInvalidator()
  const form = useForm<MediaFormValues>({
    resolver: zodResolver(mediaSchema),
    shouldUseNativeValidation: true,
    mode: "onChange",
    defaultValues: values,
  })
  const { submit } = useFormSave({
    form,
    schema: mediaSchema,
    autoSave: true,
    save: async (values) => {
      await updateMediaUnderstandingSettings({
        ocrModelId: values.ocrModelId || null,
        transcriptionModelId: values.transcriptionModelId || null,
      })
      void invalidate(resourceKeys.mediaUnderstandingSettings())
    },
    errorMessage: t("mediaUnderstanding.saveError"),
    errorFields: ["ocrModelId", "transcriptionModelId"],
    logLabel: "保存媒体理解设置",
  })

  return (
    <form
      className="w-full"
      aria-label={t("mediaUnderstanding.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="ocrModelId"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="media-ocr-model">{t("mediaUnderstanding.ocrModel")}</FieldLabel>
              <NativeSelect {...field} id="media-ocr-model">
                <option value="">{t("customerService.models.notUsed")}</option>
                <AIModelOptionGroups models={ocrModels} />
              </NativeSelect>
              <FieldDescription>
                {t("mediaUnderstanding.ocrModelDescription")}
                {ocrModels.length === 0 ? (
                  <>
                    {" "}
                    <Link to="/settings/model-services">{t("customerService.models.configureModels")}</Link>
                  </>
                ) : null}
              </FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="transcriptionModelId"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="media-transcription-model">{t("mediaUnderstanding.transcriptionModel")}</FieldLabel>
              <NativeSelect {...field} id="media-transcription-model">
                <option value="">{t("customerService.models.notUsed")}</option>
                <AIModelOptionGroups models={transcriptionModels} />
              </NativeSelect>
              <FieldDescription>
                {t("mediaUnderstanding.transcriptionModelDescription")}
                {transcriptionModels.length === 0 ? (
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
