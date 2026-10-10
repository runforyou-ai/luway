/** 企业服务器连接页。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { EntryLayout } from "@/components/entry-layout"
import { useStartup } from "@/contexts/startup-context"
import { ServerConnectionForm } from "@/features/server-connection/server-connection-form"
import { clearPendingServerLink } from "@/lib/server-link-queue"

/** 展示企业服务器地址表单；已连接服务器时可取消切换，回到当前服务器。 */
export function ServerConnectionPage() {
  const { t } = useTranslation(["connection", "common"])
  const navigate = useNavigate()
  const { connected } = useStartup()
  return (
    <EntryLayout
      title={t("title")}
      footer={
        connected ? (
          <button
            type="button"
            className="transition-colors hover:text-foreground"
            onClick={() => {
              clearPendingServerLink()
              navigate("/", { replace: true })
            }}
          >
            {t("common:actions.cancel")}
          </button>
        ) : null
      }
    >
      <ServerConnectionForm />
    </EntryLayout>
  )
}
