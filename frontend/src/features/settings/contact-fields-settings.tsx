/** 企业联系人字段：列表、新增编辑弹窗与删除确认。 */
import { useEffect } from "react"
import { ListIcon, PlusIcon, XIcon } from "lucide-react"
import { Controller, useFieldArray, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { z } from "zod"

import {
  ContactFieldType,
  createContactField,
  deleteContactField,
  listContactFields,
  updateContactField,
  type ContactField,
} from "@/api"
import { AutoGrowTextarea } from "@/components/form/auto-grow-textarea"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ResourceContent } from "@/components/resource-content"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

import { DictionaryListSettings } from "./dictionary-list-settings"

/** 联系人字段可选的类型。 */
const fieldTypes = [
  ContactFieldType.Text,
  ContactFieldType.Number,
  ContactFieldType.Date,
  ContactFieldType.Select,
] as const

/** 读取联系人字段，展示字段列表并承载新增、编辑和删除。 */
export function ContactFieldsSettings() {
  const { t } = useTranslation("settings")
  const fields = useResource(resourceKeys.contactFields(), () =>
    listContactFields(),
  )
  return (
    <ResourceContent
      resources={[fields]}
      errorMessage={t("customerService.contactFields.loadError")}
    >
      <DictionaryListSettings
        title={t("customerService.contactFields.title")}
        description={t("customerService.contactFields.description")}
        createLabel={t("customerService.contactFields.create")}
        editLabel={t("customerService.contactFields.edit")}
        dialogClassName="max-w-xl"
        nameHeader={t("customerService.contactFields.form.name")}
        rows={fields.data?.fields ?? []}
        empty={t("customerService.contactFields.empty")}
        renderRow={(field) => (
          <ResourceRowIdentity
            icon={ListIcon}
            name={field.name}
            secondary={t(`customerService.contactFields.types.${field.type}`)}
            description={
              [
                field.type === ContactFieldType.Select
                  ? field.options.map((option) => option.name).join("、")
                  : "",
                field.aiInstruction
                  ? t("customerService.contactFields.aiFilled")
                  : "",
              ]
                .filter(Boolean)
                .join(" · ") || undefined
            }
          />
        )}
        renderForm={(field, actions) => <ContactFieldForm field={field} {...actions} />}
        invalidateKeys={[resourceKeys.contactFields(), resourceKeys.contact()]}
        deletion={{
          action: (field) => deleteContactField(field.id),
          title: (name) => t("customerService.contactFields.deleteTitle", { name }),
          description: t("customerService.contactFields.deleteDescription"),
          success: t("customerService.contactFields.deleted"),
          error: t("customerService.contactFields.deleteError"),
          logLabel: "删除联系人字段",
        }}
      />
    </ResourceContent>
  )
}

/** 联系人字段表单校验规则；单选字段至少一个选项。 */
const contactFieldSchema = z.object({
  name: z.string().trim().min(1).max(50),
  type: z.enum(fieldTypes),
  options: z.array(
    z.object({
      optionId: z.string(),
      name: z.string().trim().min(1).max(50),
    }),
  ),
  aiInstruction: z.string().trim().max(500),
})

type ContactFieldFormValues = z.infer<typeof contactFieldSchema>

/** 保存新联系人字段或现有字段；类型创建后只读，单选字段可增删选项，可设置 AI 填写说明。 */
function ContactFieldForm({
  field,
  onSaved,
  onCancel,
}: {
  field?: ContactField
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation("settings")
  const form = useForm<ContactFieldFormValues>({
    resolver: zodResolver(contactFieldSchema),
    shouldUseNativeValidation: true,
    defaultValues: {
      name: field?.name ?? "",
      type: field?.type ?? ContactFieldType.Text,
      options: (field?.options ?? []).map((option) => ({
        optionId: option.id,
        name: option.name,
      })),
      aiInstruction: field?.aiInstruction ?? "",
    },
  })
  const { submit } = useFormSave({
    form,
    schema: contactFieldSchema,
    autoSave: false,
    // 保存成功即提示并刷新列表、关闭弹窗，保存期间弹窗已放弃时同样执行。
    save: async (values) => {
      // 非单选字段不提交选项。
      const input = {
        name: values.name,
        type: values.type,
        options:
          values.type === ContactFieldType.Select
            ? values.options.map((option) => ({ id: option.optionId, name: option.name }))
            : [],
        aiInstruction: values.aiInstruction,
      }
      await (field ? updateContactField(field.id, input) : createContactField(input))
      toast.success(t(field ? "customerService.contactFields.updated" : "customerService.contactFields.created"))
      onSaved()
    },
    errorMessage: t("customerService.contactFields.saveError"),
    errorFields: ["name", "type", "options", "aiInstruction"],
    logLabel: "保存联系人字段",
  })
  const options = useFieldArray({ control: form.control, name: "options" })
  const type = form.watch("type")
  useEffect(() => {
    if (!field) form.setFocus("name")
  }, [field, form])

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <FormInputField
          name="name"
          control={form.control}
          label={t("customerService.contactFields.form.name")}
          required
        />
        <Controller
          name="type"
          control={form.control}
          render={({ field: typeField }) => (
            <Field>
              <FieldLabel htmlFor={typeField.name} required>
                {t("customerService.contactFields.form.type")}
              </FieldLabel>
              <NativeSelect
                {...typeField}
                id={typeField.name}
                disabled={Boolean(field)}
                onChange={(event) => {
                  typeField.onChange(event.target.value)
                  // 切换为单选时提供第一个空选项，切换为其他类型时清空选项。
                  if (event.target.value === ContactFieldType.Select) {
                    if (options.fields.length === 0) {
                      options.append({ optionId: "", name: "" })
                    }
                  } else {
                    options.replace([])
                  }
                }}
              >
                {fieldTypes.map((value) => (
                  <option key={value} value={value}>
                    {t(`customerService.contactFields.types.${value}`)}
                  </option>
                ))}
              </NativeSelect>
              {field ? (
                <FieldDescription>
                  {t("customerService.contactFields.form.typeLocked")}
                </FieldDescription>
              ) : null}
            </Field>
          )}
        />
        {type === ContactFieldType.Select ? (
          <Field>
            <FieldLabel required>
              {t("customerService.contactFields.form.options")}
            </FieldLabel>
            <div className="grid gap-2">
              {options.fields.map((option, index) => (
                <div key={option.id} className="flex items-center gap-2">
                  <Input
                    {...form.register(`options.${index}.name`)}
                    aria-label={t("customerService.contactFields.form.optionName", {
                      index: index + 1,
                    })}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    className="shrink-0"
                    disabled={options.fields.length === 1}
                    aria-label={t("customerService.contactFields.form.removeOption")}
                    title={t("customerService.contactFields.form.removeOption")}
                    onClick={() => options.remove(index)}
                  >
                    <XIcon />
                  </Button>
                </div>
              ))}
              <div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="-ml-2"
                  onClick={() => options.append({ optionId: "", name: "" })}
                >
                  <PlusIcon />
                  {t("customerService.contactFields.form.addOption")}
                </Button>
              </div>
            </div>
            {field ? (
              <FieldDescription>
                {t("customerService.contactFields.form.optionsHelp")}
              </FieldDescription>
            ) : null}
          </Field>
        ) : null}
        <Controller
          name="aiInstruction"
          control={form.control}
          render={({ field: instructionField, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={instructionField.name}>
                {t("customerService.contactFields.form.aiInstruction")}
              </FieldLabel>
              <AutoGrowTextarea
                {...instructionField}
                id={instructionField.name}
                aria-invalid={fieldState.invalid}
              />
              <FieldDescription>
                {t("customerService.contactFields.form.aiInstructionHelp")}
              </FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
    </form>
  )
}
