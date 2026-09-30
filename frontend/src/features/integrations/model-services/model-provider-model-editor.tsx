/** 模型目录的新增、编辑、移除和校验提示。 */
import { useMemo, useState } from "react"
import { useWatch, type FieldErrors, type UseFieldArrayReturn, type UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { AIModelInputModality, AIModelType } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { FormValidationMessage } from "@/components/form/form-validation-message"
import { Button } from "@/components/ui/button"
import { ModelEditDialog } from "./model-edit-dialog"
import { ModelPickerDialog } from "./model-picker-dialog"
import { ModelProviderModelList } from "./model-provider-model-list"
import type { AIModelFormValues, AIProviderFormValues } from "./model-provider-schema"

/** 返回模型目录中的第一条字段校验提示。 */
function modelValidationMessage(errors: FieldErrors<AIProviderFormValues>["models"]) {
  if (!errors) return ""
  if (typeof errors.message === "string") return errors.message
  if (typeof errors.root?.message === "string") return errors.root.message
  if (!Array.isArray(errors)) return ""
  for (const model of errors) {
    if (!model) continue
    const error =
      model.identifier ?? model.name ?? model.contextWindow ?? model.maxOutputTokens
    if (typeof error?.message === "string") return error.message
  }
  return ""
}

/** 编辑供应商的模型目录并保留独立的弹窗状态。 */
export function ModelProviderModelEditor({ form, modelFields, mode }: {
  form: UseFormReturn<AIProviderFormValues>
  modelFields: UseFieldArrayReturn<AIProviderFormValues, "models">
  mode: "create" | "edit"
}) {
  const { t } = useTranslation("integrations")
  const [editingModel, setEditingModel] = useState<{
    index: number | null
    model: AIModelFormValues
  } | null>(null)
  const [removingModel, setRemovingModel] = useState<number | null>(null)
  const watchedModels = useWatch({ control: form.control, name: "models" })
  // 编辑中的模型不与自身标识冲突。
  const takenIdentifiers = useMemo(
    () =>
      new Set(
        watchedModels
          .filter((_, index) => index !== editingModel?.index)
          .map((model) => model.identifier.trim()),
      ),
    [watchedModels, editingModel],
  )

  /** 打开弹窗添加一个文本输入的自定义对话模型。 */
  function addCustomModel() {
    setEditingModel({
      index: null,
      model: {
        identifier: "",
        name: "",
        type: AIModelType.AIModelTypeChat,
        inputModalities: [AIModelInputModality.AIModelInputModalityText],
        contextWindow: "",
        maxOutputTokens: "",
      },
    })
  }

  /** 修改模型目录；编辑已保存的供应商时立即校验目录，不合法时显示提示并等待补全后自动保存。 */
  function changeModels(change: () => void) {
    change()
    if (mode === "edit") void form.trigger("models")
  }

  /** 保存弹窗中的模型：新增时追加，编辑时替换原位置。 */
  function saveModel(model: AIModelFormValues) {
    const index = editingModel?.index ?? null
    changeModels(() =>
      index === null ? modelFields.append(model) : modelFields.update(index, model),
    )
    setEditingModel(null)
  }

  /** 移除模型；已保存的供应商在确认后移除。 */
  function removeModel(index: number) {
    if (mode === "edit") setRemovingModel(index)
    else modelFields.remove(index)
  }

  const modelErrorMessage = modelValidationMessage(form.formState.errors.models)
  return (
    <>
      <section className="relative">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h3 className="flex items-center gap-2 text-sm font-medium">
            {t("modelServices.models.title")}
            <span aria-hidden="true" className="text-destructive">
              *
            </span>
          </h3>
          <div className="flex items-center gap-3">
            <Button
              type="button"
              variant="link"
              size="sm"
              className="h-auto p-0"
              onClick={addCustomModel}
            >
              {t("modelServices.models.manualAdd")}
            </Button>
            <ModelPickerDialog
              form={form}
              onAppend={(models) => changeModels(() => modelFields.append(models))}
            />
          </div>
        </div>
        <ModelProviderModelList
          models={modelFields.fields.map((field, index) => ({
            key: field.id,
            model: watchedModels[index] ?? field,
          }))}
          removable={mode === "create" || modelFields.fields.length > 1}
          onEdit={(index) =>
            setEditingModel({ index, model: form.getValues(`models.${index}`) })
          }
          onRemove={removeModel}
        />
        {/* 校验提示使用表单分区间距，不改变操作按钮位置。 */}
        <FormValidationMessage
          className="absolute top-full right-0 left-0 mt-2"
          message={modelErrorMessage}
        />
      </section>
      <ModelEditDialog
        editing={
          editingModel
            ? { creating: editingModel.index === null, model: editingModel.model }
            : null
        }
        takenIdentifiers={takenIdentifiers}
        onOpenChange={(open) => !open && setEditingModel(null)}
        onSave={saveModel}
      />
      <ConfirmationDialog
        open={removingModel !== null}
        pending={false}
        title={
          removingModel !== null
            ? t("modelServices.models.removeTitle", {
                name: watchedModels[removingModel]?.name ?? "",
              })
            : ""
        }
        description={t("modelServices.models.removeDescription")}
        onOpenChange={(open) => !open && setRemovingModel(null)}
        onConfirm={() => {
          if (removingModel !== null) changeModels(() => modelFields.remove(removingModel))
          setRemovingModel(null)
        }}
      />
    </>
  )
}
