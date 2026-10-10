/** 偏好设置中的当前设备通知权限状态与操作。 */
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldDescription, FieldTitle } from "@/components/ui/field"
import { useNotificationPermission } from "@/hooks/use-notification-permission"
import { NotificationPermissionStatus } from "@/platform/native"
import { canSendNotification } from "@/platform/notifications"
import { openNotificationSettings } from "@/platform/system"

/** 展示本设备通知权限，并在需要时提供申请操作。 */
export function NotificationPermissionSettings() {
  const { t } = useTranslation("settings")
  const { status, requesting, requestPermission } = useNotificationPermission()
  const authorized = status !== "checking" && canSendNotification(status)

  /** 处理当前设备的通知授权操作。 */
  async function allowNotifications() {
    let requestFailed = false
    try {
      const nextStatus = await requestPermission()
      if (!nextStatus) {
        return
      }
      if (canSendNotification(nextStatus)) {
        toast.success(t("notifications.permission.allowSuccess"))
        return
      }
      if (
        nextStatus !==
        NotificationPermissionStatus.NotificationPermissionStatusDenied
      ) {
        toast.error(t("notifications.permission.allowDenied"))
        return
      }
    } catch (error) {
      requestFailed = true
      console.warn("申请通知权限失败", error)
    }

    try {
      if (await openNotificationSettings()) {
        return
      }
    } catch (error) {
      console.warn("打开 macOS 通知设置失败", error)
      toast.error(t("notifications.permission.settingsOpenError"))
      return
    }
    toast.error(
      t(
        requestFailed
          ? "notifications.permission.allowError"
          : "notifications.permission.allowDenied",
      ),
    )
  }

  return (
    // 与通知开关卡片保持同一外观。
    <Field orientation="horizontal" className="rounded-lg border p-4">
      <FieldContent>
        <FieldTitle>
          {t("notifications.permission.label")}
        </FieldTitle>
        <FieldDescription>
          {authorized
            ? t("notifications.permission.authorizedDescription")
            : t(
                "notifications.permission.unauthorizedDescription",
              )}
        </FieldDescription>
      </FieldContent>
      <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
        {authorized ? (
          <span className="rounded-full bg-muted px-2 py-1 text-xs font-medium text-muted-foreground">
            {t("notifications.permission.authorized")}
          </span>
        ) : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={status === "checking" || requesting}
            onClick={() => void allowNotifications()}
          >
            {requesting ? <LoaderCircleIcon className="animate-spin" /> : null}
            {requesting
              ? t("notifications.permission.allowing")
              : t("notifications.permission.allow")}
          </Button>
        )}
      </div>
    </Field>
  )
}
