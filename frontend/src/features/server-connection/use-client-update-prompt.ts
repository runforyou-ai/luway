/** 桌面端连上服务器时准备客户端新版本，就绪后提示重启；不能在应用内替换时提示从下载页安装。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { ClientUpdateState, isApiError, prepareClientUpdate, realtimeClient, restartClientUpdate } from "@/api"
import { clientDownloadPath } from "@/lib/client-download"
import { apiErrorMessage } from "@/lib/form-errors"
import { resolveServerURL } from "@/lib/server-url"
import { isDesktopMainWindow } from "@/platform/desktop-window"
import { openExternalURL } from "@/platform/external-navigation"

const promptToastId = "client-update"

/** 主窗口的成员事件流每次连上服务器时准备服务器提供的客户端新版本，提示显示期间不重复弹出；关闭提示后下次连上服务器时再次提示，重启失败时提示保留，服务器不再提供新版本时收起提示。 */
export function useClientUpdatePrompt() {
  const { t, i18n } = useTranslation(["connection", "common"])
  useEffect(() => {
    let active = true
    let mainWindow = false
    let preparing = false
    let promptedVersion = ""
    // 显示新版本就绪提示，提示关闭后允许再次提示。
    const showPrompt = (version: string) => {
      promptedVersion = version
      toast(t("update.ready", { version }), {
        id: promptToastId,
        duration: Infinity,
        onDismiss: () => {
          promptedVersion = ""
        },
        action: {
          label: t("update.restart"),
          // 点击后保留提示：重启成功时应用退出，失败时可以再次重启。
          onClick: (event) => {
            event.preventDefault()
            void restartClientUpdate().catch((error: unknown) => {
              toast.error(isApiError(error) ? apiErrorMessage(error) : t("common:errors.network"))
            })
          },
        },
      })
    }
    // 显示新版本可用提示，引导从服务器下载页安装，提示关闭后允许再次提示。
    const showDownloadPrompt = (version: string) => {
      promptedVersion = version
      toast(t("update.available", { version }), {
        id: promptToastId,
        duration: Infinity,
        onDismiss: () => {
          promptedVersion = ""
        },
        action: {
          label: t("update.download"),
          // 点击操作会关闭提示但不触发 onDismiss，同样允许再次提示。
          onClick: () => {
            promptedVersion = ""
            void resolveServerURL().then((serverUrl) => {
              if (serverUrl) void openExternalURL(`${serverUrl}${clientDownloadPath(i18n.language)}`)
            })
          },
        },
      })
    }
    // 准备新版本并按结果显示或收起提示。
    const prepare = () => {
      if (!mainWindow || preparing) {
        return
      }
      preparing = true
      void prepareClientUpdate()
        .then((update) => {
          if (!active) {
            return
          }
          const ready = update.state === ClientUpdateState.ClientUpdateStateReady
          if (!ready && update.state !== ClientUpdateState.ClientUpdateStateAvailable) {
            promptedVersion = ""
            toast.dismiss(promptToastId)
            return
          }
          if (update.version !== promptedVersion) {
            if (ready) {
              showPrompt(update.version)
            } else {
              showDownloadPrompt(update.version)
            }
          }
        })
        .catch((error: unknown) => {
          console.warn("准备客户端更新失败", error)
        })
        .finally(() => {
          preparing = false
        })
    }
    void isDesktopMainWindow().then((value) => {
      mainWindow = value
      if (realtimeClient.state === "ready") {
        prepare()
      }
    })
    const unsubscribe = realtimeClient.subscribe((event) => {
      if (event.type === "state" && event.state === "ready") {
        prepare()
      }
    })
    return () => {
      active = false
      unsubscribe()
    }
  }, [t, i18n])
}
