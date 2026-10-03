/** 原生端接口版本低于服务器要求时的客户端升级页。 */
import { CircleArrowUpIcon, DownloadIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { checkClientUpdate } from "@/api"
import { EntryLayout } from "@/components/entry-layout"
import { Button } from "@/components/ui/button"
import { openClientDownloadPage, useClientUpdateInstaller } from "@/features/server-connection/client-update"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { resolveServerURL } from "@/lib/server-url"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 提示升级客户端：桌面端能应用内安装时直接更新，否则从当前服务器的下载页获取新版本，也可以更换服务器。 */
export function ClientUpgradePage() {
  const { t, i18n } = useTranslation("connection")
  const navigate = useNavigate()
  const serverUrl = useResource(resourceKeys.serverURL(), () => resolveServerURL()).data
  const update = useResource(resourceKeys.clientUpdate(), checkClientUpdate, {
    enabled: resolveAppPlatform() === "desktop",
  }).data
  const installer = useClientUpdateInstaller()
  const installable = Boolean(update?.installable) && !installer.failed
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
      {installable ? (
        <Button
          type="button"
          className="w-full"
          disabled={installer.installing}
          onClick={() => void installer.install()}
        >
          <CircleArrowUpIcon />
          {installer.installing ? installer.pendingLabel : t("update.install")}
        </Button>
      ) : (
        <Button
          type="button"
          className="w-full"
          disabled={!serverUrl}
          onClick={() => void openClientDownloadPage(i18n.language)}
        >
          <DownloadIcon />
          {t("upgrade.download")}
        </Button>
      )}
    </EntryLayout>
  )
}
