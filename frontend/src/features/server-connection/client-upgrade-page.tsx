/** 原生端接口版本低于服务器要求时的客户端升级页。 */
import { DownloadIcon, LoaderCircleIcon, RotateCwIcon } from "lucide-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError } from "@/api"
import { serverURL } from "@/api/client"
import { EntryLayout } from "@/components/entry-layout"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { clientDownloadPath } from "@/lib/client-download"
import { apiErrorMessage } from "@/lib/form-errors"
import { openExternalURL } from "@/platform/system"
import { ClientUpdateState, prepareClientUpdate, restartClientUpdate } from "@/platform/native"

/** 提示升级客户端：能从当前服务器更新时下载新版本后重启，并保留服务器下载页作为手动安装入口；不能更新时只打开下载页，也可以更换服务器。 */
export function ClientUpgradePage() {
  const { t, i18n } = useTranslation(["connection", "common"])
  const navigate = useNavigate()
  const serverUrl = serverURL()
  const update = useResource(resourceKeys.clientUpdate(serverUrl), () => prepareClientUpdate(serverUrl), {
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

  const ready = update.data?.state === ClientUpdateState.ClientUpdateStateReady
  // 新版本就绪时下载页作为次要操作保留，重启更新失败后可以手动安装。
  const download = (
    <Button
      type="button"
      variant={ready ? "outline" : "default"}
      className="w-full"
      disabled={!serverUrl}
      onClick={() => void openExternalURL(`${serverUrl}${clientDownloadPath(i18n.language)}`)}
    >
      <DownloadIcon />
      {t("upgrade.download")}
    </Button>
  )
  let action: React.ReactNode = download
  if (ready) {
    action = (
      <div className="grid gap-3">
        <Button type="button" className="w-full" disabled={restarting} onClick={() => void restart()}>
          {restarting ? <LoaderCircleIcon className="animate-spin" /> : <RotateCwIcon />}
          {t("update.restart")}
        </Button>
        {download}
      </div>
    )
  } else if (update.loading) {
    action = (
      <Button type="button" className="w-full" disabled>
        <LoaderCircleIcon className="animate-spin" />
        {t("upgrade.downloading")}
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
