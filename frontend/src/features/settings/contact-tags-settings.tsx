/** 企业联系人标签：列表、新增编辑弹窗与删除确认。 */
import { useEffect, useMemo } from "react"
import { TagsIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  createContactTag,
  deleteContactTag,
  isApiError,
  listContactTags,
  updateContactTag,
  type ContactTag,
} from "@/api"
import { AutoGrowTextarea } from "@/components/form/auto-grow-textarea"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ResourceContent } from "@/components/resource-content"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

import { DictionaryListSettings } from "./dictionary-list-settings"

/** 读取联系人标签，展示标签列表并承载新增、编辑和删除。 */
export function ContactTagsSettings() {
  const { t } = useTranslation("settings")
  const tags = useResource(resourceKeys.contactTags(), () => listContactTags())
  return (
    <ResourceContent
      resources={[tags]}
      errorMessage={t("customerService.contactTags.loadError")}
    >
      <DictionaryListSettings
        title={t("customerService.contactTags.title")}
        description={t("customerService.contactTags.description")}
        createLabel={t("customerService.contactTags.create")}
        editLabel={t("customerService.contactTags.edit")}
        dialogClassName="max-w-md"
        nameHeader={t("customerService.contactTags.form.name")}
        rows={tags.data?.tags ?? []}
        empty={t("customerService.contactTags.empty")}
        renderRow={(tag) => (
          <ResourceRowIdentity
            icon={TagsIcon}
            name={tag.name}
            description={
              tag.aiInstruction
                ? t("customerService.contactTags.aiCondition", {
                    condition: tag.aiInstruction,
                  })
                : undefined
            }
          />
        )}
        renderForm={(tag, actions) => <ContactTagForm tag={tag} {...actions} />}
        invalidateKeys={[resourceKeys.contactTags(), resourceKeys.contact(), resourceKeys.contacts()]}
        deletion={{
          action: (tag) => deleteContactTag(tag.id),
          title: (name) => t("customerService.contactTags.deleteTitle", { name }),
          description: t("customerService.contactTags.deleteDescription"),
          success: t("customerService.contactTags.deleted"),
          error: t("customerService.contactTags.deleteError"),
          logLabel: "删除联系人标签",
        }}
      />
    </ResourceContent>
  )
}

/** 保存新联系人标签或现有标签的名称与 AI 添加条件。 */
function ContactTagForm({
  tag,
  onSaved,
  onCancel,
}: {
  tag?: ContactTag
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation("settings")
  const navigate = useNavigate()
  const schema = useMemo(
    () =>
      z.object({
        name: z
          .string()
          .trim()
          .min(1, t("customerService.contactTags.validation.nameRequired"))
          .max(30, t("customerService.contactTags.validation.nameTooLong")),
        aiInstruction: z
          .string()
          .trim()
          .max(500, t("customerService.contactTags.validation.aiInstructionTooLong")),
      }),
    [t],
  )
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { name: tag?.name ?? "", aiInstruction: tag?.aiInstruction ?? "" },
  })
  useEffect(() => {
    form.setFocus("name")
  }, [form])

  /** 提交联系人标签表单。 */
  async function submit(values: z.infer<typeof schema>) {
    try {
      if (tag) {
        await updateContactTag(tag.id, values)
      } else {
        await createContactTag(values)
      }
      toast.success(
        t(
          tag
            ? "customerService.contactTags.updated"
            : "customerService.contactTags.created",
        ),
      )
      onSaved()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("保存联系人标签失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["name", "aiInstruction"])
          : t("customerService.contactTags.saveError"),
      )
    }
  }

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <FormInputField
          name="name"
          control={form.control}
          label={t("customerService.contactTags.form.name")}
          required
        />
        <Controller
          name="aiInstruction"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name}>
                {t("customerService.contactTags.form.aiInstruction")}
              </FieldLabel>
              <AutoGrowTextarea
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              />
              <FieldDescription>
                {t("customerService.contactTags.form.aiInstructionHelp")}
              </FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
    </form>
  )
}
