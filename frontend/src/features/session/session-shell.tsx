/** 登录外壳：读取登录身份并分流会话入口，建立本窗口的实时连接，装配登录期共享上下文。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Navigate } from "react-router"

import type { Identity } from "@/api"
import { PageLoading } from "@/components/page-loading"
import { PageLoadError } from "@/components/page-load-error"
import { RealtimeSyncProvider } from "@/contexts/realtime-sync-context"
import { UserPreferencesProvider } from "@/contexts/user-preferences"
import { AttachmentQueueProvider } from "@/contexts/attachment-queue-context"
import { OutgoingMessageProvider } from "@/contexts/outgoing-message-context"
import { ComposerDraftProvider } from "@/contexts/composer-draft-context"
import { useIdentityLoader } from "@/features/session/use-identity-loader"
import { useRealtimeConnection } from "@/features/session/use-realtime-connection"

/** 身份就绪后把登录身份交给宿主渲染，未登录或需要切换入口时跳转；restartOnResume 在回到前台时重建实时事件流。 */
export function SessionShell({
  restartOnResume = false,
  children,
}: {
  restartOnResume?: boolean
  children: (identity: Identity) => ReactNode
}) {
  const { t } = useTranslation(["workspace", "common"])
  const { status, identity, redirectPath, retry } = useIdentityLoader()
  useRealtimeConnection(Boolean(identity?.user.id), { restartOnResume })

  if (status === "anonymous") return <Navigate to="/login" replace />
  if (status === "redirect" && redirectPath) {
    return <Navigate to={redirectPath} replace />
  }
  if (status === "failed") {
    return <PageLoadError message={t("identityLoadError")} onRetry={retry} />
  }
  if (!identity) {
    return (
      <PageLoading />
    )
  }

  return (
    <UserPreferencesProvider user={identity.user}>
      <OutgoingMessageProvider key={identity.user.id}>
        <ComposerDraftProvider key={identity.user.id}>
          <RealtimeSyncProvider>
            <AttachmentQueueProvider key={identity.user.id}>
              {children(identity)}
            </AttachmentQueueProvider>
          </RealtimeSyncProvider>
        </ComposerDraftProvider>
      </OutgoingMessageProvider>
    </UserPreferencesProvider>
  )
}
