/** 用户通知设置表单。 */
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import {
  updateUserPreferences,
  type CurrentUser,
} from "@/api"
import { SwitchField } from "@/components/form/switch-field"
import { FieldGroup } from "@/components/ui/field"
import { NotificationPermissionSettings } from "@/components/notification-permission-settings"
import {
  createNotificationSettingsSchema,
  type NotificationSettingsFormValues,
} from "@/features/settings/notification-settings-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResourceInvalidator } from "@/hooks/use-resource"
import {
  readNotificationDevicePreferences,
  setNotificationSoundEnabled,
  type NotificationDeviceScope,
} from "@/platform/notifications"
import { zodResolver } from "@/lib/zod-resolver"

/** 修改新消息提醒、本机通知声音，并管理本设备通知权限。 */
export function NotificationSettingsForm({ user }: { user: CurrentUser }) {
  const { t } = useTranslation(["settings", "common"])
  const invalidate = useResourceInvalidator()
  const notificationScope = useMemo<NotificationDeviceScope>(
    () => ({ organizationId: user.organizationId, userId: user.id }),
    [user.id, user.organizationId],
  )
  const schema = useMemo(() => createNotificationSettingsSchema(), [])
  const form = useForm<NotificationSettingsFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      messageNotificationsEnabled: user.messageNotificationsEnabled,
      notificationSoundEnabled:
        readNotificationDevicePreferences(notificationScope).soundEnabled,
    },
  })
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: true,
    save: async (values) => {
      // 账号偏好接口需要完整提交，语言与时区沿用当前值。
      const updated = await updateUserPreferences({
        locale: user.locale,
        translationLanguage: user.translationLanguage,
        timeZone: user.timeZone,
        messageNotificationsEnabled: values.messageNotificationsEnabled,
      })
      setNotificationSoundEnabled(
        notificationScope,
        values.notificationSoundEnabled,
      )
      void invalidate(resourceKeys.identity())
      return {
        messageNotificationsEnabled: updated.messageNotificationsEnabled,
        notificationSoundEnabled: values.notificationSoundEnabled,
      }
    },
    savedValues: (saved) => saved,
    errorMessage: t("notifications.saveError"),
    errorFields: ["messageNotificationsEnabled"],
    logLabel: "保存通知设置",
  })

  return (
    <form
      className="w-full"
      aria-label={t("notifications.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="messageNotificationsEnabled"
          control={form.control}
          render={({ field }) => (
            <SwitchField
              id={field.name}
              name={field.name}
              label={t("notifications.newMessages")}
              description={t("notifications.newMessagesDescription")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
        <Controller
          name="notificationSoundEnabled"
          control={form.control}
          render={({ field }) => (
            <SwitchField
              id={field.name}
              name={field.name}
              label={t("notifications.sound")}
              description={t("notifications.soundDescription")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
        <NotificationPermissionSettings />
      </FieldGroup>
    </form>
  )
}
