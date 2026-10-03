/** 桌面端应用内更新：定时检查所连接服务器提供的客户端版本，发现新版本时提示，确认后下载安装并重启，不能应用内安装时打开下载页。 */
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { checkClientUpdate, installClientUpdate, onClientUpdateProgress, type ClientUpdate } from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceReader } from "@/hooks/use-resource"
import { clientDownloadPath } from "@/lib/client-download"
import { requestErrorMessage } from "@/lib/form-errors"
import { resolveServerURL } from "@/lib/server-url"
import { openExternalURL } from "@/platform/external-navigation"

// 自动检查的间隔。
const checkInterval = 6 * 60 * 60 * 1000

// 本次运行中用户选择稍后的版本，重新挂载后不再自动提示该版本。
let dismissedVersion = ""

/** 打开所连接服务器的客户端下载页。 */
export async function openClientDownloadPage(language: string) {
  const serverUrl = await resolveServerURL()
  if (serverUrl) {
    await openExternalURL(`${serverUrl}${clientDownloadPath(language)}`)
  }
}

/** 返回安装更新的进行状态与按钮文案；安装成功后应用随即重启，失败时提示错误并标记为不可应用内安装。 */
export function useClientUpdateInstaller() {
  const { t } = useTranslation("connection")
  const [installing, setInstalling] = useState(false)
  const [failed, setFailed] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)

  useEffect(() => {
    if (!installing) return
    return onClientUpdateProgress(({ written, total }) => {
      setProgress(total > 0 ? Math.min(100, Math.floor((written * 100) / total)) : null)
    })
  }, [installing])

  /** 下载、验证并安装更新。 */
  async function install() {
    setInstalling(true)
    setProgress(null)
    try {
      await installClientUpdate()
    } catch (error) {
      toast.error(requestErrorMessage(error))
      setFailed(true)
      setInstalling(false)
    }
  }

  const pendingLabel =
    progress === null ? t("update.preparing") : progress < 100 ? t("update.downloading", { progress }) : t("update.installing")
  return { installing, failed, pendingLabel, install }
}

/** 返回自动与手动检查更新的方法及更新提示对话框；enabled 为 false 时不检查。 */
export function useClientUpdate(enabled: boolean) {
  const { t } = useTranslation("connection")
  const readResource = useResourceReader()
  const resource = useResource(resourceKeys.clientUpdate(), checkClientUpdate, {
    enabled,
    refetchInterval: checkInterval,
    refetchIntervalInBackground: true,
    refetchOnWindowFocus: false,
  })
  const [prompted, setPrompted] = useState<ClientUpdate | null>(null)
  const update = resource.data

  // 发现用户未选择稍后的新版本时自动提示。
  useEffect(() => {
    if (update?.version && update.version !== dismissedVersion) {
      setPrompted(update)
    }
  }, [update])

  /** 立即检查更新，有新版本时提示，已是最新时告知。 */
  async function check() {
    try {
      const result = await readResource(resourceKeys.clientUpdate(), checkClientUpdate)
      if (result.version) {
        setPrompted(result)
      } else {
        toast.success(t("update.upToDate"))
      }
    } catch (error) {
      toast.error(requestErrorMessage(error))
    }
  }

  const dialog = prompted ? (
    <ClientUpdateDialog
      update={prompted}
      onClose={() => {
        dismissedVersion = prompted.version
        setPrompted(null)
      }}
    />
  ) : null
  return { update, check, dialog }
}

/** 提示新版本，确认后在应用内安装或打开下载页。 */
function ClientUpdateDialog({ update, onClose }: { update: ClientUpdate; onClose: () => void }) {
  const { t, i18n } = useTranslation("connection")
  const installer = useClientUpdateInstaller()
  const installable = update.installable && !installer.failed
  const values = { version: update.version, current: update.currentVersion }

  return (
    <ConfirmationDialog
      open
      pending={installer.installing}
      destructive={false}
      title={t("update.title")}
      description={t(installable ? "update.description" : "update.downloadDescription", values)}
      pendingLabel={installer.pendingLabel}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      onConfirm={() => {
        if (installable) {
          void installer.install()
          return
        }
        void openClientDownloadPage(i18n.language)
        onClose()
      }}
    />
  )
}
