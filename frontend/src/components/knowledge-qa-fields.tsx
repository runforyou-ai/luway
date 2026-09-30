/** 知识库问答的标准问题、相似问题和答案表单字段，供问答编辑页与待补知识共用。 */
import { useId } from "react"
import { Controller, useFieldArray, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { z } from "zod"

import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"

/** 生成问答表单的必填校验。 */
export function createQASchema(messages: {
  question: string
  answer: string
}) {
  return z.object({
    question: z.string().trim().min(1, messages.question),
    similarQuestions: z.array(
      z.object({ id: z.string(), content: z.string() }),
    ),
    answer: z.string().trim().min(1, messages.answer),
  })
}

export type QAFormValues = z.infer<ReturnType<typeof createQASchema>>

/** 管理带稳定业务编号的多条相似问题输入。 */
function SimilarQuestionFields({
  control,
  disabled,
}: {
  control: Control<QAFormValues>
  disabled: boolean
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const id = useId()
  const { fields, append, remove } = useFieldArray({
    control,
    name: "similarQuestions",
    keyName: "fieldKey",
  })
  return (
    <div className="space-y-4" role="group" aria-labelledby={`${id}-label`}>
      <div id={`${id}-label`} className="text-sm font-medium">
        {t("qa.similarQuestions")}
      </div>
      {fields.map((item, index) => (
        <div className="flex items-end gap-3" key={item.fieldKey}>
          <div className="min-w-0 flex-1">
            <FormInputField
              control={control}
              name={`similarQuestions.${index}.content`}
              required={false}
              id={`${id}-${item.fieldKey}`}
              label={t("qa.similarQuestion", { number: index + 1 })}
              disabled={disabled}
            />
          </div>
          <Button
            type="button"
            variant="outline"
            // 与表单页加高后的输入框同高，并排时底边对齐。
            className="h-11 rounded-lg"
            disabled={disabled}
            aria-label={t("qa.removeSimilarQuestion", { number: index + 1 })}
            onClick={() => remove(index)}
          >
            {t("common:actions.remove")}
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={disabled}
        onClick={() => append({ id: "", content: "" })}
      >
        {t("qa.addSimilarQuestion")}
      </Button>
    </div>
  )
}

/** 展示标准问题、相似问题和答案输入。 */
export function QAFormFields({
  control,
  disabled,
  answerRows = 12,
}: {
  control: Control<QAFormValues>
  disabled: boolean
  answerRows?: number
}) {
  const { t } = useTranslation("knowledgeBase")
  const id = useId()
  return (
    <FieldGroup>
      <FormInputField
        control={control}
        name="question"
        id={`${id}-question`}
        label={t("qa.question")}
        required
        disabled={disabled}
      />
      <SimilarQuestionFields control={control} disabled={disabled} />
      <Controller
        control={control}
        name="answer"
        render={({ field, fieldState }) => (
          <Field>
            <FieldLabel htmlFor={`${id}-answer`} required>
              {t("qa.answer")}
            </FieldLabel>
            <Textarea
              {...field}
              id={`${id}-answer`}
              required
              rows={answerRows}
              aria-invalid={fieldState.invalid}
              disabled={disabled}
            />
          </Field>
        )}
      />
    </FieldGroup>
  )
}
