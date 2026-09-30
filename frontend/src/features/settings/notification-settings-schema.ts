/** 通知设置表单校验规则。 */
import { z } from "zod"

/** 创建通知设置表单校验。 */
export function createNotificationSettingsSchema() {
  return z.object({
    messageNotificationsEnabled: z.boolean(),
    notificationSoundEnabled: z.boolean(),
  })
}

export type NotificationSettingsFormValues = z.infer<
  ReturnType<typeof createNotificationSettingsSchema>
>
