/** 修改密码表单。 */
import { useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { changePassword, isApiError } from "@/api"
import { recoverSession } from "@/lib/session-navigation"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  createChangePasswordSchema,
  type ChangePasswordFormValues,
} from "@/features/settings/change-password-schema"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { apiErrorMessage } from "@/lib/form-errors"
import { zodResolver } from "@/lib/zod-resolver"

/** 修改当前用户的登录密码，移动端使用触屏尺寸的整行提交按钮。 */
export function ChangePasswordForm() {
  const { t } = useTranslation("settings")
  const navigate = useNavigate()
  const schema = useMemo(() => createChangePasswordSchema(t), [t])
  const form = useForm<ChangePasswordFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      currentPassword: "",
      newPassword: "",
      confirmPassword: "",
    },
  })
  useFormLifetime(form.formState.isDirty)
  /** 提交密码修改。 */
  async function save(values: ChangePasswordFormValues) {
    try {
      await changePassword({
        currentPassword: values.currentPassword,
        newPassword: values.newPassword,
      })
      form.reset()
      toast.success(t("password.saveSuccess"))
    } catch (error) {
      if (recoverSession(error, navigate)) {
        return
      }
      console.warn("修改密码失败", error)
      if (isApiError(error)) {
        toast.error(
          apiErrorMessage(error, ["currentPassword", "newPassword"]),
        )
        return
      }
      toast.error(t("password.saveError"))
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form
      className="w-full space-y-9"
      aria-label={t("password.formLabel")}
      onSubmit={form.handleSubmit(save)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="currentPassword"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("password.currentPassword")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                type="password"
                autoComplete="current-password"
                aria-invalid={fieldState.invalid}
                required
              />
            </Field>
          )}
        />
        <Controller
          name="newPassword"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("password.newPassword")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                type="password"
                autoComplete="new-password"
                aria-invalid={fieldState.invalid}
                required
              />
            </Field>
          )}
        />
        <Controller
          name="confirmPassword"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("password.confirmPassword")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                type="password"
                autoComplete="new-password"
                aria-invalid={fieldState.invalid}
                required
              />
            </Field>
          )}
        />
      </FieldGroup>
      <div className="flex justify-end">
        <Button
          type="submit"
          className="touch:min-h-11 touch:w-full"
          disabled={isSubmitting}
        >
          {isSubmitting ? (
            <LoaderCircleIcon className="animate-spin" />
          ) : null}
          {isSubmitting ? t("password.saving") : t("password.save")}
        </Button>
      </div>
    </form>
  )
}
