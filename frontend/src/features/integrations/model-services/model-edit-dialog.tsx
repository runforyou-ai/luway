/** 模型服务表单中新增或编辑单个模型的弹窗。 */
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import { AIModelType } from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import {
  createAIModelSchema,
  type AIModelFormValues,
} from "@/components/model-provider/model-provider-schema"
import {
  modelInputModalityNameKeys,
  modelInputModalityOrder,
  modelTypeNameKeys,
  modelTypeOrder,
} from "@/components/model-provider/model-service-options"
import { useAIModelSchemaMessages } from "@/components/model-provider/use-ai-model-schema-messages"
import { zodResolver } from "@/lib/zod-resolver"

/** editing 非空时打开弹窗编辑模型草稿，保存后把模型交给 onSave。 */
export function ModelEditDialog({
  editing,
  takenIdentifiers,
  onOpenChange,
  onSave,
}: {
  editing: { creating: boolean; model: AIModelFormValues } | null
  takenIdentifiers: ReadonlySet<string>
  onOpenChange: (open: boolean) => void
  onSave: (model: AIModelFormValues) => void
}) {
  return (
    <Dialog open={editing !== null} onOpenChange={onOpenChange}>
      {editing ? (
        <ModelEditDialogContent
          creating={editing.creating}
          model={editing.model}
          takenIdentifiers={takenIdentifiers}
          onCancel={() => onOpenChange(false)}
          onSave={onSave}
        />
      ) : null}
    </Dialog>
  )
}

/** 维护单个模型的独立表单。 */
function ModelEditDialogContent({
  creating,
  model,
  takenIdentifiers,
  onCancel,
  onSave,
}: {
  creating: boolean
  model: AIModelFormValues
  takenIdentifiers: ReadonlySet<string>
  onCancel: () => void
  onSave: (model: AIModelFormValues) => void
}) {
  const { t } = useTranslation(["integrations", "common"])
  const messages = useAIModelSchemaMessages()
  const schema = useMemo(
    () => createAIModelSchema(messages, takenIdentifiers),
    [messages, takenIdentifiers],
  )
  const form = useForm<AIModelFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: model,
  })
  const isChat = form.watch("type") === AIModelType.Chat

  return (
    <DialogContent className="max-w-xl" aria-describedby={undefined}>
      <DialogHeader>
        <DialogTitle>
          {t(
            creating
              ? "modelServices.models.createTitle"
              : "modelServices.models.editTitle",
          )}
        </DialogTitle>
      </DialogHeader>
      <form
        className="space-y-9"
        noValidate
        onSubmit={form.handleSubmit((values) =>
          onSave({
            ...values,
            maxOutputTokens:
              values.type === AIModelType.Chat
                ? values.maxOutputTokens
                : "",
          }),
        )}
      >
        <FieldGroup>
          <Controller
            name="type"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor="model-type" required>
                  {t("modelServices.models.columns.type")}
                </FieldLabel>
                <NativeSelect
                  {...field}
                  id="model-type"
                  required
                  aria-invalid={fieldState.invalid}
                >
                  {modelTypeOrder.map((type) => (
                    <option key={type} value={type}>
                      {t(modelTypeNameKeys[type])}
                    </option>
                  ))}
                </NativeSelect>
              </Field>
            )}
          />
          <FormInputField
            name="identifier"
            id="model-identifier"
            control={form.control}
            label={t("modelServices.models.columns.identifier")}
            maxLength={200}
            autoComplete="off"
            autoFocus={creating}
            className="font-mono"
          />
          <FormInputField
            name="name"
            id="model-name"
            control={form.control}
            label={t("modelServices.models.columns.name")}
            maxLength={200}
            autoComplete="off"
          />
          <Field>
            <FieldLabel id="model-input-modalities" required>
              {t("modelServices.models.columns.inputModalities")}
            </FieldLabel>
            <div
              role="group"
              aria-labelledby="model-input-modalities"
              className="flex flex-wrap gap-x-5 gap-y-2"
            >
              {modelInputModalityOrder.map((modality) => (
                <label
                  key={modality}
                  className="inline-flex items-center gap-2 text-sm"
                >
                  <input
                    {...form.register("inputModalities")}
                    type="checkbox"
                    value={modality}
                    className="size-4 accent-primary"
                  />
                  {t(modelInputModalityNameKeys[modality])}
                </label>
              ))}
            </div>
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormInputField
              name="contextWindow"
              id="model-context-window"
              control={form.control}
              label={t("modelServices.models.columns.contextWindow")}
              inputMode="decimal"
              autoComplete="off"
            />
            {isChat ? (
              <FormInputField
                name="maxOutputTokens"
                id="model-max-output-tokens"
                control={form.control}
                label={t("modelServices.models.columns.maxOutputTokens")}
                inputMode="decimal"
                autoComplete="off"
              />
            ) : null}
          </div>
        </FieldGroup>
        <FormActions saving={false} onCancel={onCancel} />
      </form>
    </DialogContent>
  )
}
