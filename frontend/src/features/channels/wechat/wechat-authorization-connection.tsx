/** 授权接入公众号渠道的授权状态、公众号资料与接口凭据状态。 */
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  checkWechatAuthorizationChannelConnection,
  startWechatAuthorization,
  WechatAuthorizationStatus,
  WechatPermission,
  type WechatAuthorizationChannel,
} from "@/api"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { WechatCredentialStatus } from "@/features/channels/wechat/wechat-credential-status"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { openResolvedExternalURL } from "@/platform/system"

/** 授权状态对应的徽标样式与文案键。 */
const statusBadges = {
  [WechatAuthorizationStatus.WechatAuthorizationActive]: { variant: "success", label: "wechatAuthorization.statuses.active" },
  [WechatAuthorizationStatus.WechatAuthorizationRevoked]: { variant: "destructive", label: "wechatAuthorization.statuses.revoked" },
  [WechatAuthorizationStatus.WechatAuthorizationPlatformChanged]: {
    variant: "warning",
    label: "wechatAuthorization.statuses.platform_changed",
  },
} as const

/** 权限集对应的名称文案键。 */
const permissionLabels = {
  [WechatPermission.Message]: "wechatAuthorization.permissions.message",
  [WechatPermission.User]: "wechatAuthorization.permissions.user",
  [WechatPermission.Material]: "wechatAuthorization.permissions.material",
} as const

/** 展示公众号授权与接口凭据状态，在浏览器中发起授权或重新授权。 */
export function WechatAuthorizationConnection({
  channel,
  onUpdated,
}: {
  channel: WechatAuthorizationChannel
  onUpdated: () => void
}) {
  const { t } = useTranslation("channels")
  const reportError = useRequestErrorReporter()
  const { connection } = channel
  const status = connection.status
  const badge = status ? statusBadges[status as keyof typeof statusBadges] : null
  const start = useMutation({
    mutationFn: (channelId: string) =>
      openResolvedExternalURL(async () => (await startWechatAuthorization(channelId)).url),
    onSuccess: () => toast.success(t("wechatAuthorization.started")),
    onError: (error, channelId) =>
      reportError(error, {
        log: "发起公众号授权",
        context: { channel_id: channelId },
        fallback: t("wechatAuthorization.startError"),
      }),
  })
  // 已授权时说明缺少的权限，授权失效时说明原因。
  const missing = connection.missingPermissions.map((permission) =>
    t(permissionLabels[permission as keyof typeof permissionLabels]),
  )
  const statusHelp =
    status === WechatAuthorizationStatus.WechatAuthorizationRevoked
      ? t("wechatAuthorization.statusHelp.revoked")
      : status === WechatAuthorizationStatus.WechatAuthorizationPlatformChanged
        ? t("wechatAuthorization.statusHelp.platform_changed")
        : ""

  return (
    <div className="w-full space-y-12">
      <section className="space-y-6">
        <div className="space-y-1">
          <h2 className="text-base font-medium">{t("wechatAuthorization.title")}</h2>
          <p className="text-sm text-muted-foreground">{t("wechatAuthorization.help")}</p>
        </div>
        <FieldGroup>
          {status ? (
            <Field>
              <FieldLabel>{t("wechatAuthorization.account")}</FieldLabel>
              <div className="flex min-w-0 items-center gap-3">
                {connection.headImageUrl ? (
                  <img
                    src={connection.headImageUrl}
                    alt=""
                    referrerPolicy="no-referrer"
                    className="size-10 shrink-0 rounded-full bg-muted object-cover"
                  />
                ) : null}
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{connection.nickName}</div>
                  <div className="truncate text-sm text-muted-foreground">
                    {[connection.principalName, connection.userName, connection.appId].filter(Boolean).join(" · ")}
                  </div>
                </div>
              </div>
            </Field>
          ) : null}
          <Field>
            <FieldLabel>{t("wechatAuthorization.status")}</FieldLabel>
            <div className="flex min-h-11 flex-wrap items-center justify-between gap-3">
              {badge ? (
                <StatusBadge variant={badge.variant}>{t(badge.label)}</StatusBadge>
              ) : (
                <StatusBadge variant="muted">{t("wechatAuthorization.statuses.none")}</StatusBadge>
              )}
              <Button
                type="button"
                variant={status ? "outline" : "default"}
                className="touch:min-h-11"
                disabled={start.isPending || !connection.platformConfigured}
                onClick={() => start.mutate(channel.id)}
              >
                {start.isPending ? <LoaderCircleIcon className="animate-spin" /> : null}
                {start.isPending
                  ? t("wechatAuthorization.starting")
                  : status
                    ? t("wechatAuthorization.reauthorize")
                    : t("wechatAuthorization.authorize")}
              </Button>
            </div>
            {!connection.platformConfigured ? (
              <FieldDescription className="text-destructive">{t("wechatAuthorization.platformMissing")}</FieldDescription>
            ) : statusHelp ? (
              <FieldDescription className="text-destructive">{statusHelp}</FieldDescription>
            ) : status && missing.length > 0 ? (
              <FieldDescription className="text-warning">
                {t("wechatAuthorization.missingPermissions", { permissions: missing.join(t("wechatAuthorization.permissionSeparator")) })}
              </FieldDescription>
            ) : null}
          </Field>
        </FieldGroup>
      </section>
      {status === WechatAuthorizationStatus.WechatAuthorizationActive ? (
        <section className="space-y-6">
          <h2 className="text-base font-medium">{t("wechatConnection.statusTitle")}</h2>
          <FieldGroup>
            <WechatCredentialStatus
              channelId={channel.id}
              state={connection}
              check={checkWechatAuthorizationChannelConnection}
              onUpdated={onUpdated}
            />
          </FieldGroup>
        </section>
      ) : null}
    </div>
  )
}
