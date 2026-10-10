/** 个人资料设置表单。 */
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { FilePurpose, updateProfile, type CurrentUser } from "@/api"
import { ImagePicker } from "@/components/image-picker"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  createProfileSettingsSchema,
  type ProfileSettingsFormValues,
} from "@/features/settings/profile-settings-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAutoSave } from "@/hooks/use-auto-save"
import { usePendingImageUpload } from "@/hooks/use-pending-image-upload"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 修改当前用户的头像、姓名和邮箱，移动端使用触屏尺寸的整行保存按钮。 */
export function ProfileSettingsForm({ user }: { user: CurrentUser }) {
  const { t } = useTranslation(["settings", "common"])
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const avatar = usePendingImageUpload({
    purpose: FilePurpose.UserAvatar,
    onError: (error) => {
      console.warn("上传用户头像失败", error)
      if (!recoverSession(error, navigate)) toast.error(t("profile.avatarUploadError"))
    },
  })
  const pendingAvatar = avatar.pending
  const schema = useMemo(() => createProfileSettingsSchema(t), [t])
  const form = useForm<ProfileSettingsFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      displayName: user.displayName,
      email: user.email,
    },
  })
  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save })

  /** 保存个人资料并刷新当前身份。 */
  async function save(values: ProfileSettingsFormValues) {
    const avatarFileId = await avatar.ensureUploaded()
    if (avatarFileId === null) return false
    try {
      const updated = await updateProfile({ ...values, avatarFileId })
      const next = {
        displayName: updated.displayName,
        email: updated.email,
      }
      acceptSaved(values, next)
      avatar.clear(avatarFileId)
      void invalidate(resourceKeys.identity())
      return true
    } catch (error) {
      reportError(error, {
        log: "保存个人资料",
        fallback: t("profile.saveError"),
        fields: ["displayName", "email"],
      })
      return false
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form
      className="w-full"
      aria-label={t("profile.formLabel")}
      onSubmit={form.handleSubmit(() => saveNow(true))}
      noValidate
    >
      <FieldGroup>
        <Field>
          <FieldLabel>{t("profile.avatar")}</FieldLabel>
          <ImagePicker
            imageURL={pendingAvatar?.previewURL || user.avatarUrl}
            name={user.displayName}
            fallback="person"
            label={t("profile.avatarChoose")}
            className="rounded-full"
            avatarClassName="rounded-full"
            disabled={isSubmitting}
            loading={pendingAvatar?.status === "uploading"}
            onSelect={(file) => {
              avatar.select(file)
              saveNow(true)
            }}
          />
        </Field>
        <Controller
          name="displayName"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("profile.displayName")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                autoComplete="name"
                aria-invalid={fieldState.invalid}
                required
              />
            </Field>
          )}
        />
        <Controller
          name="email"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("profile.email")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                type="email"
                autoComplete="email"
                aria-invalid={fieldState.invalid}
                required
              />
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}
