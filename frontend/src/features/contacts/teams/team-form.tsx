/** 新建和编辑团队表单。 */
import { useEffect, useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { createTeam, updateTeam, type Team } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { FormActions } from "@/components/form/form-actions"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { useFormSave } from "@/hooks/use-form-save"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { teamMembershipCacheKeys } from "@/features/contacts/teams/team-membership-cache"
import {
  createTeamSchema,
  type TeamFormValues,
} from "@/features/contacts/teams/team-schema"
import { zodResolver } from "@/lib/zod-resolver"

/** 保存新团队或现有团队。 */
export function TeamForm({
  team,
  onSaved,
  onCancel,
}: {
  team?: Team
  onSaved: (team: Team) => void
  onCancel?: () => void
}) {
  const { t } = useTranslation(["contacts", "common"])
  const invalidate = useResourceInvalidator()
  const schema = useMemo(
    () =>
      createTeamSchema({
        nameRequired: t("teams.validation.nameRequired"),
        nameTooLong: t("teams.validation.nameTooLong"),
        descriptionTooLong: t("teams.validation.descriptionTooLong"),
      }),
    [t],
  )
  const form = useForm<TeamFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      name: team?.name ?? "",
      description: team?.description ?? "",
    },
  })
  useEffect(() => {
    if (!team) form.setFocus("name")
  }, [form, team])

  const { submit } = useFormSave({
    form,
    schema,
    autoSave: false,
    save: async (values) => {
      const saved = team
        ? await updateTeam(team.id, values)
        : await createTeam(values)
      void invalidate(resourceKeys.teams())
      void invalidate(resourceKeys.serviceCategories())
      for (const key of teamMembershipCacheKeys) void invalidate(key)
      return saved
    },
    onSubmitted: (saved) => {
      toast.success(t(team ? "teams.form.updated" : "teams.form.created"))
      onSaved(saved)
    },
    errorFields: ["name", "description"],
    logLabel: "保存团队",
  })

  return (
    <form
      className="space-y-9"
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup className="gap-5">
        <FormInputField
          name="name"
          control={form.control}
          label={t("teams.form.name")}
        />
        <Controller
          name="description"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name}>
                {t("teams.form.description")}
              </FieldLabel>
              <Textarea
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              />
            </Field>
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
    </form>
  )
}
