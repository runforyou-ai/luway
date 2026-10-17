/** 平台支援模式横幅：提示当前以支援会话进入工作区，可退出并返回后台。 */
import { useTranslation } from "react-i18next"

import { revokeSupportAccess } from "@/api"
import { clearToken } from "@/api/client"
import { Button } from "@/components/ui/button"
import { adminWorkspacesPath } from "@/features/admin/admin-paths"
import { beginSessionBoundary } from "@/lib/resource-client"
import { navigateToHashPath } from "@/lib/workspace-route"

/** 支援模式下展示在用户端壳顶栏。 */
export function SupportBanner() {
  const { t } = useTranslation("admin")

  async function exitSupport() {
    try {
      await revokeSupportAccess()
    } catch (error) {
      console.warn("撤销支援会话失败", error)
    }
    clearToken()
    beginSessionBoundary()
    navigateToHashPath(adminWorkspacesPath, { replace: true })
  }

  return (
    <div className="flex items-center justify-between gap-3 border-b border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm text-amber-950 dark:text-amber-100">
      <p>{t("support.banner")}</p>
      <Button size="sm" variant="outline" onClick={() => void exitSupport()}>
        {t("support.exit")}
      </Button>
    </div>
  )
}
