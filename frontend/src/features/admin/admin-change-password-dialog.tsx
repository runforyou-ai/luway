/** 平台后台修改当前管理员密码的对话框。 */
import { useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { changeAdminPassword } from "@/api"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  createChangePasswordSchema,
  type ChangePasswordFormValues,
} from "@/features/settings/change-password-schema"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { zodResolver } from "@/lib/zod-resolver"

/** 提交修改密码；成功后关闭对话框。状态应挂在账号菜单外，避免随菜单关闭卸载。 */
export function AdminChangePasswordDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation(["admin", "settings", "common"])
  const reportError = useRequestErrorReporter()
  const schema = useMemo(
    () => createChangePasswordSchema((key) => t(`settings:${key}`)),
    [t],
  )
  const form = useForm<ChangePasswordFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      currentPassword: "",
      newPassword: "",
      confirmPassword: "",
    },
  })

  async function save(values: ChangePasswordFormValues) {
    try {
      await changeAdminPassword({
        currentPassword: values.currentPassword,
        newPassword: values.newPassword,
      })
      form.reset()
      toast.success(t("settings:password.saveSuccess"))
      onOpenChange(false)
    } catch (error) {
      reportError(error, {
        log: "修改平台管理员密码",
        fallback: t("settings:password.saveError"),
        fields: ["currentPassword", "newPassword"],
      })
    }
  }

  const { isSubmitting } = form.formState

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) form.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t("admin:shell.changePassword")}</DialogTitle>
          <DialogDescription>{t("admin:shell.changePasswordDescription")}</DialogDescription>
        </DialogHeader>
        <form className="space-y-9" aria-label={t("settings:password.formLabel")} onSubmit={form.handleSubmit(save)} noValidate>
          <FieldGroup>
            <Controller
              name="currentPassword"
              control={form.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <FieldLabel htmlFor={`admin-${field.name}`} required>
                    {t("settings:password.currentPassword")}
                  </FieldLabel>
                  <Input
                    {...field}
                    id={`admin-${field.name}`}
                    type="password"
                    autoComplete="current-password"
                    aria-invalid={fieldState.invalid}
                    required
                    autoFocus
                  />
                </Field>
              )}
            />
            <Controller
              name="newPassword"
              control={form.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <FieldLabel htmlFor={`admin-${field.name}`} required>
                    {t("settings:password.newPassword")}
                  </FieldLabel>
                  <Input
                    {...field}
                    id={`admin-${field.name}`}
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
                  <FieldLabel htmlFor={`admin-${field.name}`} required>
                    {t("settings:password.confirmPassword")}
                  </FieldLabel>
                  <Input
                    {...field}
                    id={`admin-${field.name}`}
                    type="password"
                    autoComplete="new-password"
                    aria-invalid={fieldState.invalid}
                    required
                  />
                </Field>
              )}
            />
          </FieldGroup>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={isSubmitting}>
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
              {isSubmitting ? t("settings:password.saving") : t("settings:password.save")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
