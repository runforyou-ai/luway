/** 桌面端连上服务器时准备客户端新版本，就绪后提示重启。 */
import { useEffect } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { ClientUpdateState, isApiError, prepareClientUpdate, realtimeClient, restartClientUpdate } from "@/api"
import { apiErrorMessage } from "@/lib/form-errors"
import { isDesktopMainWindow } from "@/platform/desktop-window"

const promptToastId = "client-update"

/** 主窗口的成员事件流每次连上服务器时准备服务器提供的客户端新版本，提示显示期间不重复弹出；关闭提示后下次连上服务器时再次提示，重启失败时立即重新提示，服务器不再提供新版本时收起提示。 */
export function useClientUpdatePrompt() {
  const { t } = useTranslation(["connection", "common"])
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
          onClick: () => {
            void restartClientUpdate().catch((error: unknown) => {
              toast.error(isApiError(error) ? apiErrorMessage(error) : t("common:errors.network"))
              showPrompt(version)
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
          if (update.state !== ClientUpdateState.ClientUpdateStateReady) {
            promptedVersion = ""
            toast.dismiss(promptToastId)
            return
          }
          if (update.version !== promptedVersion) {
            showPrompt(update.version)
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
  }, [t])
}
