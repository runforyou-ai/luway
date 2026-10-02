/** 邀请落地页：展示邀请信息，未登录时引导登录或注册，已登录时由当前账号接受邀请。 */
import { useEffect, useState } from "react"
import { LoaderCircleIcon, MailIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  InvitationStatus,
  acceptInvitation,
  isApiError,
  logout,
  previewInvitation,
} from "@/api"
import { EntryLayout } from "@/components/entry-layout"
import { PageLoading } from "@/components/page-loading"
import { PageLoadError } from "@/components/page-load-error"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAccountSession } from "@/hooks/use-account-session"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { clearPendingInvitation, rememberPendingInvitation } from "@/lib/pending-invitation"
import { enterWorkspace } from "@/lib/workspace-route"

/** 按链接中的令牌读取邀请并根据登录状态给出下一步。 */
export function InvitationPage() {
  const { t } = useTranslation("account")
  const { token = "" } = useParams()
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [accepting, setAccepting] = useState(false)
  const [switching, setSwitching] = useState(false)
  const [alreadyMember, setAlreadyMember] = useState(false)
  const preview = useResource(resourceKeys.invitationPreview(token), (signal) => previewInvitation({ token }, signal), {
    staleTime: 0,
  })
  const session = useAccountSession()
  const signedOut = session.signedOut
  const pending = preview.data?.status === InvitationStatus.InvitationStatusPending
  // 令牌不存在与已撤销、已过期的邀请一样展示失效。
  const invalid = (isApiError(preview.error) && preview.error.kind === "not_found") || (preview.data !== undefined && !pending)

  // 未登录时记住邀请，登录或注册完成后回到这里。
  useEffect(() => {
    if (signedOut && pending) rememberPendingInvitation(token)
  }, [signedOut, pending, token])

  if (session.redirectPath) return <Navigate to={session.redirectPath} replace />

  /** 由当前账号接受邀请并进入工作区。 */
  async function accept() {
    setAccepting(true)
    try {
      const workspace = await acceptInvitation({ token })
      clearPendingInvitation()
      void invalidate(resourceKeys.workspaces())
      enterWorkspace(workspace.slug)
    } catch (error) {
      setAccepting(false)
      if (isApiError(error) && error.reason === "invitation_already_member") {
        clearPendingInvitation()
        setAlreadyMember(true)
        return
      }
      console.warn("接受邀请失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("invitation.acceptError"))
    }
  }

  /** 退出当前账号后换用受邀邮箱的账号登录。 */
  async function switchAccount() {
    setSwitching(true)
    rememberPendingInvitation(token)
    try {
      await logout()
    } catch (error) {
      console.warn("退出登录失败", error)
    }
    navigate("/login", { replace: true })
  }

  if (invalid) {
    return (
      <EntryLayout title={t("invitation.invalidTitle")} description={t("invitation.invalidDescription")}>
        <Button variant="outline" className="w-full" asChild>
          <Link to="/" replace>
            {t("invitation.goHome")}
          </Link>
        </Button>
      </EntryLayout>
    )
  }
  if (preview.error && !preview.data && !preview.retrying) {
    return <PageLoadError message={t("invitation.loadError")} onRetry={preview.refresh} />
  }
  if (!preview.data || (!session.account && !session.error)) {
    return (
      <PageLoading />
    )
  }

  const { workspaceName, workspaceSlug, inviterName, maskedEmail } = preview.data
  if (alreadyMember) {
    return (
      <EntryLayout title={t("invitation.memberTitle", { workspace: workspaceName })} description={t("invitation.memberDescription")}>
        <Button className="w-full" onClick={() => enterWorkspace(workspaceSlug)}>
          {t("invitation.enter")}
        </Button>
      </EntryLayout>
    )
  }
  return (
    <EntryLayout
      title={t("invitation.title", { workspace: workspaceName })}
      description={t("invitation.description", { inviter: inviterName, email: maskedEmail })}
    >
      {session.account ? (
        <div className="space-y-3">
          <div className="flex items-center gap-3 rounded-xl border bg-card px-4 py-3 text-sm">
            <MailIcon className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">{t("signedInAs", { email: session.account.email })}</span>
            <button
              type="button"
              className="shrink-0 text-xs font-medium text-foreground/80 underline-offset-4 hover:text-foreground hover:underline disabled:opacity-50"
              disabled={accepting || switching}
              onClick={() => void switchAccount()}
            >
              {t("invitation.switchAccount")}
            </button>
          </div>
          <Button className="w-full" disabled={accepting || switching} onClick={() => void accept()}>
            {accepting ? <LoaderCircleIcon className="animate-spin" /> : null}
            {accepting ? t("invitation.accepting") : t("invitation.accept")}
          </Button>
        </div>
      ) : (
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">{t("invitation.loginHint")}</p>
          <Button className="w-full" asChild>
            <Link to="/login">{t("invitation.login")}</Link>
          </Button>
          <Button variant="outline" className="w-full" asChild>
            <Link to={`/register?invitation=${encodeURIComponent(token)}`}>{t("invitation.register")}</Link>
          </Button>
        </div>
      )}
    </EntryLayout>
  )
}
