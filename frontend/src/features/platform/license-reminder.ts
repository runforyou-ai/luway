/** 授权续期提醒：计算授权剩余天数，平台后台按授权状态每天提醒一次，直到当天关闭提醒。 */
import { useEffect, useRef } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { getLicense, LicenseStatus, type License } from "@/api"
import { currentSessionGeneration } from "@/api/session-scope"
import { adminLicensePath } from "@/features/admin/admin-paths"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { readLocalPreference, writeLocalPreference } from "@/lib/local-preference"

/** 授权到期前开始提醒续期的天数。 */
export const renewalReminderDays = 30

/** 授权提醒的轻提示标识。 */
const reminderToastId = "license-reminder"

/** 本机当天已关闭的授权提醒标记。 */
const reminderStorageKey = "app.licenseReminder"

/** 工作台中重新读取授权的间隔毫秒数，跨日后据此重新判断是否提醒。 */
const reminderRefreshInterval = 60 * 60 * 1000

/** 返回有效授权距到期的天数，不足一天按一天计。 */
export function licenseRemainingDays(license: License) {
  return Math.max(1, Math.ceil((new Date(license.expiresAt ?? 0).getTime() - Date.now()) / 86_400_000))
}

/** 平台后台中，授权已到期或临近到期时提醒并提供授权页入口；用户关闭或查看后当天同一原因不再提醒。 */
export function useLicenseReminder() {
  const { t } = useTranslation("platform")
  const navigate = useNavigate()
  const license = useResource(resourceKeys.license(), (signal) => getLicense(signal), {
    staleTime: 0,
    refetchInterval: reminderRefreshInterval,
    refetchOnWindowFocus: true,
    recoverSession: false,
  })
  const current = license.data
  const checkedAt = license.dataUpdatedAt
  const refreshing = license.refreshing
  // visible 记录提醒是否正在显示，closingByApp 标记由程序关闭的提醒，这类关闭不计为用户关闭。
  const visible = useRef(false)
  const closingByApp = useRef(false)

  useEffect(() => {
    if (!current || refreshing) return
    let reason: "expired" | "expiring" | null = null
    if (current.status === LicenseStatus.None) reason = null
    else if (current.status === LicenseStatus.Expired) reason = "expired"
    else if (licenseRemainingDays(current) <= renewalReminderDays) reason = "expiring"
    // 按服务器、提醒原因和本地日期记录当天已关闭的提醒。
    const now = new Date()
    const marker = `${current.deploymentId}:${reason}:${now.getFullYear()}-${now.getMonth() + 1}-${now.getDate()}`
    if (!reason || readLocalPreference(reminderStorageKey) === marker) {
      if (visible.current) {
        closingByApp.current = true
        toast.dismiss(reminderToastId)
      }
      return
    }
    const messages = {
      expired: t("license.statusHelp.expired"),
      expiring: t("license.statusHelp.expiring", { count: licenseRemainingDays(current) }),
    }
    const generation = currentSessionGeneration()
    visible.current = true
    toast.warning(messages[reason], {
      id: reminderToastId,
      duration: Infinity,
      // 切换工作区或登录会话时关闭的提醒，以及提醒条件消失后由程序关闭的提醒，不计为已关闭。
      onDismiss: () => {
        visible.current = false
        if (closingByApp.current) {
          closingByApp.current = false
          return
        }
        if (currentSessionGeneration() === generation) writeLocalPreference(reminderStorageKey, marker)
      },
      action: {
        label: t("license.view"),
        onClick: () => {
          visible.current = false
          writeLocalPreference(reminderStorageKey, marker)
          navigate(adminLicensePath)
        },
      },
    })
  }, [current, checkedAt, refreshing, navigate, t])
}
