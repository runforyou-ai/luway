/** 企业咨询分类目录：列表、新增编辑弹窗与删除确认。 */
import { useEffect, useMemo } from "react"
import { TagIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  createServiceCategory,
  deleteServiceCategory,
  isApiError,
  listAllTeams,
  listServiceCategories,
  updateServiceCategory,
  type ServiceCategory,
  type Team,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ResourceContent } from "@/components/resource-content"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

import { DictionaryListSettings } from "./dictionary-list-settings"

/** 读取咨询分类与团队，展示分类列表并承载新增、编辑和删除。 */
export function ServiceCategoriesSettings() {
  const { t } = useTranslation("settings")
  const categories = useResource(resourceKeys.serviceCategories(), () =>
    listServiceCategories(),
  )
  const teams = useResource(resourceKeys.teams({ all: true }), () =>
    listAllTeams(),
  )
  return (
    <ResourceContent
      resources={[categories, teams]}
      errorMessage={t("customerService.categories.loadError")}
    >
      <DictionaryListSettings
        description={t("customerService.categories.description")}
        createLabel={t("customerService.categories.create")}
        editLabel={t("customerService.categories.edit")}
        formDescription={t("customerService.categories.formDescription")}
        dialogClassName="max-w-xl"
        nameHeader={t("customerService.categories.form.name")}
        rows={categories.data?.categories ?? []}
        empty={t("customerService.categories.empty")}
        renderRow={(category) => (
          <ResourceRowIdentity
            icon={TagIcon}
            name={category.name}
            secondary={
              category.team?.name ??
              t("customerService.categories.channelFallback")
            }
            description={category.description || undefined}
          />
        )}
        renderForm={(category, actions) => (
          <ServiceCategoryForm category={category} teams={teams.data ?? []} {...actions} />
        )}
        invalidateKeys={[resourceKeys.serviceCategories()]}
        deletion={{
          action: (category) => deleteServiceCategory(category.id),
          title: (name) => t("customerService.categories.deleteTitle", { name }),
          description: t("customerService.categories.deleteDescription"),
          success: t("customerService.categories.deleted"),
          error: t("customerService.categories.deleteError"),
          logLabel: "删除咨询分类",
        }}
      />
    </ResourceContent>
  )
}

/** 创建咨询分类表单校验规则。 */
function createServiceCategorySchema(messages: {
  nameRequired: string
  nameTooLong: string
  descriptionTooLong: string
}) {
  return z.object({
    name: z.string().trim().min(1, messages.nameRequired).max(64, messages.nameTooLong),
    description: z.string().trim().max(500, messages.descriptionTooLong),
    teamId: z.string(),
  })
}

type ServiceCategoryFormValues = z.infer<
  ReturnType<typeof createServiceCategorySchema>
>

/** 保存新咨询分类或现有咨询分类。 */
function ServiceCategoryForm({
  category,
  teams,
  onSaved,
  onCancel,
}: {
  category?: ServiceCategory
  teams: Team[]
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation("settings")
  const navigate = useNavigate()
  const schema = useMemo(
    () =>
      createServiceCategorySchema({
        nameRequired: t("customerService.categories.validation.nameRequired"),
        nameTooLong: t("customerService.categories.validation.nameTooLong"),
        descriptionTooLong: t(
          "customerService.categories.validation.descriptionTooLong",
        ),
      }),
    [t],
  )
  const form = useForm<ServiceCategoryFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      name: category?.name ?? "",
      description: category?.description ?? "",
      teamId: category?.team?.id ?? "",
    },
  })
  // 登记未保存状态，弹窗关闭前确认放弃修改。
  useFormLifetime(form.formState.isDirty)
  useEffect(() => {
    if (!category) form.setFocus("name")
  }, [category, form])

  /** 提交咨询分类表单，团队为空表示按渠道失败路由。 */
  async function submit(values: ServiceCategoryFormValues) {
    const input = {
      name: values.name,
      description: values.description,
      teamId: values.teamId || null,
    }
    try {
      if (category) {
        await updateServiceCategory(category.id, input)
      } else {
        await createServiceCategory(input)
      }
      toast.success(
        t(
          category
            ? "customerService.categories.updated"
            : "customerService.categories.created",
        ),
      )
      onSaved()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("保存咨询分类失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["name", "description", "teamId"])
          : t("customerService.categories.saveError"),
      )
    }
  }

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <FormInputField
          name="name"
          control={form.control}
          label={t("customerService.categories.form.name")}
          required
        />
        <Controller
          name="description"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name}>
                {t("customerService.categories.form.description")}
              </FieldLabel>
              <Textarea
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              />
              <FieldDescription>
                {t("customerService.categories.form.descriptionHelp")}
              </FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="teamId"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor={field.name}>
                {t("customerService.categories.form.team")}
              </FieldLabel>
              <NativeSelect {...field} id={field.name}>
                <option value="">
                  {t("customerService.categories.channelFallback")}
                </option>
                {teams.map((team) => (
                  <option key={team.id} value={team.id}>
                    {team.name}
                  </option>
                ))}
              </NativeSelect>
              <FieldDescription>
                {t("customerService.categories.form.teamHelp")}
              </FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
    </form>
  )
}
