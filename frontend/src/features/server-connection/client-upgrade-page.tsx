/** 原生端接口版本低于服务器要求时的客户端升级页。 */
import { DownloadIcon, LoaderCircleIcon, RotateCwIcon } from "lucide-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { ClientUpdateState, isApiError, prepareClientUpdate, restartClientUpdate } from "@/api"
import { EntryLayout } from "@/components/entry-layout"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { clientDownloadPath } from "@/lib/client-download"
import { apiErrorMessage } from "@/lib/form-errors"
import { resolveServerURL } from "@/lib/server-url"
import { openExternalURL } from "@/platform/external-navigation"

/** 提示升级客户端：能从当前服务器更新时下载新版本后重启，否则打开服务器的下载页，也可以更换服务器。 */
export function ClientUpgradePage() {
  const { t, i18n } = useTranslation(["connection", "common"])
  const navigate = useNavigate()
  const serverUrl = useResource(resourceKeys.serverURL(), () => resolveServerURL()).data
  const update = useResource(resourceKeys.clientUpdate(serverUrl), () => prepareClientUpdate(), {
    enabled: Boolean(serverUrl),
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  })
  const [restarting, setRestarting] = useState(false)

  // 以新版本重启，失败时留在当前页。
  const restart = async () => {
    setRestarting(true)
    try {
      await restartClientUpdate()
    } catch (error) {
      setRestarting(false)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("common:errors.network"))
    }
  }

  let action: React.ReactNode
  if (update.data?.state === ClientUpdateState.ClientUpdateStateReady) {
    action = (
      <Button type="button" className="w-full" disabled={restarting} onClick={() => void restart()}>
        {restarting ? <LoaderCircleIcon className="animate-spin" /> : <RotateCwIcon />}
        {t("update.restart")}
      </Button>
    )
  } else if (update.loading) {
    action = (
      <Button type="button" className="w-full" disabled>
        <LoaderCircleIcon className="animate-spin" />
        {t("upgrade.downloading")}
      </Button>
    )
  } else {
    // 当前端不能从服务器更新、服务器没有更新包或下载失败时，从下载页手动安装。
    action = (
      <Button
        type="button"
        className="w-full"
        disabled={!serverUrl}
        onClick={() => void openExternalURL(`${serverUrl}${clientDownloadPath(i18n.language)}`)}
      >
        <DownloadIcon />
        {t("upgrade.download")}
      </Button>
    )
  }

  return (
    <EntryLayout
      title={t("upgrade.title")}
      description={serverUrl ? t("upgrade.description", { host: new URL(serverUrl).host }) : null}
      footer={
        <button
          type="button"
          className="transition-colors hover:text-foreground"
          onClick={() => navigate("/connect")}
        >
          {t("upgrade.changeServer")}
        </button>
      }
    >
      {action}
    </EntryLayout>
  )
}
