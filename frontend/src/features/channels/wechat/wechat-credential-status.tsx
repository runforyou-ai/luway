/** 公众号渠道接口凭据的可用状态与重新检测。 */
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { WechatTokenFailure } from "@/api"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"

/** 接口凭据状态中的失败原因与详情。 */
type TokenFailureState = {
  tokenFailure: WechatTokenFailure | null
  tokenFailureDetail: string
}

/** 展示接口凭据可用性与最近一次失败原因，提供重新检测接口凭据。 */
export function WechatCredentialStatus({
  channelId,
  state,
  check,
  onUpdated,
}: {
  channelId: string
  state: TokenFailureState
  check: (channelId: string) => Promise<{ connection: TokenFailureState }>
  onUpdated: () => void
}) {
  const { t } = useTranslation("channels")
  const reportError = useRequestErrorReporter()
  const checkMutation = useMutation({
    mutationFn: check,
    onSuccess: (checked) => {
      // 检测完成但凭据仍获取失败时提示查看失败原因。
      if (checked.connection.tokenFailure) toast.error(t("wechatConnection.checkFailed"))
      else toast.success(t("wechatConnection.checked"))
    },
    onError: (error, id) =>
      reportError(error, {
        log: "检测公众号凭据",
        context: { channel_id: id },
        fallback: t("wechatConnection.checkError"),
      }),
    onSettled: () => onUpdated(),
  })
  // 最近一次获取失败时显示失败，其余情况凭据在调用微信接口前按需续期。
  const credential = state.tokenFailure
    ? { label: t("wechatConnection.credential.failed"), variant: "destructive" as const }
    : { label: t("wechatConnection.credential.ready"), variant: "success" as const }
  // 按失败原因组织说明。
  let failureText = ""
  if (state.tokenFailure === WechatTokenFailure.IPNotWhitelisted) {
    failureText = state.tokenFailureDetail
      ? t("wechatConnection.failures.ip_not_whitelisted", { ip: state.tokenFailureDetail })
      : t("wechatConnection.failures.ip_not_whitelisted_unknown")
  } else if (state.tokenFailure === WechatTokenFailure.Rejected) {
    failureText = t("wechatConnection.failures.rejected", { detail: state.tokenFailureDetail })
  } else if (state.tokenFailure) {
    failureText = t("wechatConnection.failures.unavailable", { detail: state.tokenFailureDetail })
  }

  return (
    <Field>
      <FieldLabel>{t("wechatConnection.credentialStatus")}</FieldLabel>
      <div className="flex min-h-11 flex-wrap items-center justify-between gap-3">
        <StatusBadge variant={credential.variant}>{credential.label}</StatusBadge>
        <Button
          type="button"
          variant="outline"
          className="touch:min-h-11"
          disabled={checkMutation.isPending}
          onClick={() => checkMutation.mutate(channelId)}
        >
          {checkMutation.isPending ? <LoaderCircleIcon className="animate-spin" /> : null}
          {checkMutation.isPending ? t("wechatConnection.checking") : t("wechatConnection.check")}
        </Button>
      </div>
      {failureText ? <FieldDescription className="text-destructive">{failureText}</FieldDescription> : null}
    </Field>
  )
}
