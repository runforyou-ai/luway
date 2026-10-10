/** 渠道绑定落地页：展示要绑定的外部账号，未登录时引导登录，已登录时由当前账号确认绑定。 */
import { useEffect, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon, MailIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  ChannelBindingStatus,
  confirmChannelBinding,
  isApiError,
  logout,
  previewChannelBinding,
} from "@/api"
import { EntryLayout } from "@/components/entry-layout"
import { PageLoadError } from "@/components/page-load-error"
import { PageLoading } from "@/components/page-loading"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAccountSession } from "@/hooks/use-account-session"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { messageChannelTypeDefinition } from "@/lib/message-channel-types"
import {
  channelBindingPath,
  clearPendingAccountPage,
  rememberPendingAccountPage,
} from "@/lib/pending-account-page"
import { enterWorkspace } from "@/lib/workspace-route"

/** 按链接中的令牌读取绑定信息并根据登录状态给出下一步。 */
export function ChannelBindingPage() {
  const { t } = useTranslation(["account", "channels"])
  const { token = "" } = useParams()
  const navigate = useNavigate()
  const [switching, setSwitching] = useState(false)
  // 确认成功后清除待返回的绑定页并展示完成状态。
  const confirmation = useMutation({
    mutationFn: () => confirmChannelBinding({ token }),
    onSuccess: () => clearPendingAccountPage(),
  })
  const confirming = confirmation.isPending
  const done = confirmation.isSuccess
  const preview = useResource(
    resourceKeys.channelBindingPreview(token),
    (signal) => previewChannelBinding({ token }, signal),
    { staleTime: 0 },
  )
  const session = useAccountSession()
  const pending = preview.data?.status === ChannelBindingStatus.Pending
  // 令牌不存在与已使用、已过期的链接一样展示失效。
  const invalid =
    !done &&
    ((isApiError(preview.error) && preview.error.kind === "not_found") ||
      (preview.data !== undefined && !pending))

  // 未登录时记住绑定链接，登录完成后回到这里。
  useEffect(() => {
    if (session.signedOut && pending) rememberPendingAccountPage(channelBindingPath(token))
  }, [session.signedOut, pending, token])

  if (session.redirectPath) return <Navigate to={session.redirectPath} replace />

  /** 由当前账号确认绑定。 */
  function confirm() {
    confirmation.mutate(undefined, {
      onError: (error) => {
        console.warn("确认渠道绑定失败", error)
        toast.error(isApiError(error) ? apiErrorMessage(error) : t("channelBinding.confirmError"))
      },
    })
  }

  /** 退出当前账号后换用工作区成员账号登录。 */
  async function switchAccount() {
    setSwitching(true)
    rememberPendingAccountPage(channelBindingPath(token))
    try {
      await logout()
    } catch (error) {
      console.warn("退出登录失败", error)
    }
    navigate("/login", { replace: true })
  }

  if (invalid) {
    return (
      <EntryLayout title={t("channelBinding.invalidTitle")} description={t("channelBinding.invalidDescription")}>
        <Button variant="outline" className="w-full" asChild>
          <Link to="/" replace>
            {t("channelBinding.goHome")}
          </Link>
        </Button>
      </EntryLayout>
    )
  }
  if (preview.error && !preview.data && !preview.retrying) {
    return <PageLoadError message={t("channelBinding.loadError")} onRetry={preview.refresh} />
  }
  if (!preview.data || (!session.account && !session.error)) {
    return <PageLoading />
  }

  const { workspaceName, workspaceSlug, channelType, externalName } = preview.data
  const definition = messageChannelTypeDefinition(channelType)
  const channel = definition ? t(`channels:types.${definition.translationKey}`) : ""
  if (done) {
    return (
      <EntryLayout title={t("channelBinding.doneTitle")} description={t("channelBinding.doneDescription", { channel })}>
        <Button variant="outline" className="w-full" onClick={() => enterWorkspace(workspaceSlug)}>
          {t("channelBinding.enter")}
        </Button>
      </EntryLayout>
    )
  }
  return (
    <EntryLayout
      title={t("channelBinding.title", { channel })}
      description={t("channelBinding.description", { channel, external: externalName, workspace: workspaceName })}
    >
      {session.account ? (
        <div className="space-y-3">
          <div className="flex items-center gap-3 rounded-xl border bg-card px-4 py-3 text-sm">
            <MailIcon className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">{t("signedInAs", { email: session.account.email })}</span>
            <button
              type="button"
              className="shrink-0 text-xs font-medium text-foreground/80 underline-offset-4 hover:text-foreground hover:underline disabled:opacity-50"
              disabled={confirming || switching}
              onClick={() => void switchAccount()}
            >
              {t("channelBinding.switchAccount")}
            </button>
          </div>
          <Button className="w-full" disabled={confirming || switching} onClick={confirm}>
            {confirming ? <LoaderCircleIcon className="animate-spin" /> : null}
            {confirming ? t("channelBinding.confirming") : t("channelBinding.confirm")}
          </Button>
        </div>
      ) : (
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">{t("channelBinding.loginHint", { workspace: workspaceName })}</p>
          <Button className="w-full" asChild>
            <Link to="/login">{t("channelBinding.login")}</Link>
          </Button>
        </div>
      )}
    </EntryLayout>
  )
}
