/** 工作区通用设置表单。 */
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import { updateWorkspace, type CurrentWorkspace } from "@/api"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspaceAddress } from "@/components/workspace-address"
import {
  generalSettingsSchema,
  type GeneralSettingsFormValues,
} from "@/features/settings/general-settings-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 显示并修改当前工作区的名称，只读展示完整访问地址。 */
export function GeneralSettingsForm({
  workspace,
}: {
  workspace: CurrentWorkspace
}) {
  const { t } = useTranslation(["settings", "common"])
  const invalidate = useResourceInvalidator()

  const form = useForm<GeneralSettingsFormValues>({
    resolver: zodResolver(generalSettingsSchema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      name: workspace.name,
    },
  })
  const { submit } = useFormSave({
    form,
    schema: generalSettingsSchema,
    autoSave: true,
    save: async (values) => {
      const saved = await updateWorkspace(values)
      void invalidate(resourceKeys.identity())
      void invalidate(resourceKeys.workspaces())
      return saved
    },
    savedValues: (saved) => ({ name: saved.name }),
    errorMessage: t("general.saveError"),
    errorFields: ["name"],
    logLabel: "工作区通用设置更新",
  })
  const address = useWorkspaceAddress(workspace.slug)

  return (
    <form
      className="w-full"
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="name"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("general.form.name")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                autoComplete="organization"
                aria-invalid={fieldState.invalid}
                required
              />
            </Field>
          )}
        />
        {/* 访问地址由部署地址和工作区标识组成，这里只读展示，可选中复制。 */}
        <Field>
          <FieldLabel htmlFor="general-address">
            {t("general.form.address")}
          </FieldLabel>
          <Input
            id="general-address"
            value={address}
            readOnly
            className="text-muted-foreground"
          />
        </Field>
      </FieldGroup>
    </form>
  )
}
