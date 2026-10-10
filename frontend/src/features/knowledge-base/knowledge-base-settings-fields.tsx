/** 在知识库表单中展示独立的模型与处理参数。 */
import { Controller, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import type { AIModelOption } from "@/api"
import { AIModelOptionGroups } from "@/components/ai-model-options"
import { FormInputField } from "@/components/form/form-input-field"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { knowledgeEmbeddingDimensions, type KnowledgeBaseFormValues } from "./knowledge-base-schema"

/** 渲染向量模型、数值参数和可清空的重排模型选择。 */
export function KnowledgeBaseSettingsFields({ control, isQA, embeddingModels, rerankModels }: {
  control: Control<KnowledgeBaseFormValues>
  isQA: boolean
  embeddingModels: AIModelOption[]
  rerankModels: AIModelOption[]
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  return (
    <>
      <KnowledgeModelField control={control} name="embeddingModelId" models={embeddingModels} />
      <Controller control={control} name="embeddingDimension" render={({ field, fieldState }) => (
        <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor="embeddingDimension" required>{t("form.embeddingDimension")}</FieldLabel>
          <NativeSelect {...field} id="embeddingDimension" required aria-invalid={fieldState.invalid}>
            <option value="">{t("form.selectEmbeddingDimension")}</option>
            {knowledgeEmbeddingDimensions.map((dimension) => (
              <option key={dimension} value={dimension}>{dimension}</option>
            ))}
          </NativeSelect>
        </Field>
      )} />
      {!isQA && (
        <>
          <FormInputField control={control} name="chunkLength" label={t("form.chunkLength")} type="number" min={256} max={2048} step={1} />
          <FormInputField control={control} name="chunkOverlap" label={t("form.chunkOverlap")} type="number" min={0} max={200} step={1} />
        </>
      )}
      <FormInputField control={control} name="retrievalCount" label={t("form.retrievalCount")} type="number" min={1} max={20} step={1} />
      <Controller control={control} name="retrievalScoreThreshold" render={({ field, fieldState }) => (
        <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor="retrievalScoreThreshold" required>{t("form.retrievalScoreThreshold")}</FieldLabel>
          <Input {...field} id="retrievalScoreThreshold" type="number" min={0} max={1} step={0.01} required aria-invalid={fieldState.invalid} />
          <FieldDescription>{t("form.retrievalScoreThresholdDescription")}</FieldDescription>
        </Field>
      )} />
      <KnowledgeModelField control={control} name="rerankModelId" models={rerankModels} />
    </>
  )
}

/** 按供应商展示满足向量或重排用途的知识库模型选项。 */
function KnowledgeModelField({ control, name, models }: {
  control: Control<KnowledgeBaseFormValues>
  name: "embeddingModelId" | "rerankModelId"
  models: AIModelOption[]
}) {
  const { t } = useTranslation("knowledgeBase")
  const embedding = name === "embeddingModelId"
  return (
    <Controller control={control} name={name} render={({ field, fieldState }) => (
      <Field data-invalid={fieldState.invalid}>
        <FieldLabel htmlFor={name} required>{t(embedding ? "form.embeddingModel" : "form.rerankModel")}</FieldLabel>
        <NativeSelect {...field} id={name} required aria-invalid={fieldState.invalid}>
          <option value="">{t(embedding ? "form.selectEmbeddingModel" : "form.selectRerankModel")}</option>
          <AIModelOptionGroups models={models} />
        </NativeSelect>
        {models.length === 0 && <FieldDescription>{t(embedding ? "form.noEmbeddingModels" : "form.noRerankModels")}</FieldDescription>}
      </Field>
    )} />
  )
}
