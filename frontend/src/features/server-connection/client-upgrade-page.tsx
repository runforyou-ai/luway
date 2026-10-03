/** 原生端接口版本低于服务器要求时的客户端升级页。 */
import { DownloadIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { EntryLayout } from "@/components/entry-layout"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { clientDownloadPath } from "@/lib/client-download"
import { resolveServerURL } from "@/lib/server-url"
import { openExternalURL } from "@/platform/external-navigation"

/** 提示升级客户端，从当前服务器的下载页获取新版本，或更换服务器。 */
export function ClientUpgradePage() {
  const { t, i18n } = useTranslation("connection")
  const navigate = useNavigate()
  const serverUrl = useResource(resourceKeys.serverURL(), () => resolveServerURL()).data
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
      <Button
        type="button"
        className="w-full"
        disabled={!serverUrl}
        onClick={() => void openExternalURL(`${serverUrl}${clientDownloadPath(i18n.language)}`)}
      >
        <DownloadIcon />
        {t("upgrade.download")}
      </Button>
    </EntryLayout>
  )
}
