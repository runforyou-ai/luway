/** 企业微信智能机器人的长连接凭据与连接状态表单。 */
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  ChannelConnectionStatus,
  isApiError,
  isNotFoundApiError,
  saveWeComBotChannelConnection,
  type WeComBotChannel,
} from "@/api"
import { DetailEditRow } from "@/components/form/detail-edit-row"
import { InlineEditField } from "@/components/form/inline-edit-field"
import { Button } from "@/components/ui/button"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useReturnTo } from "@/hooks/use-return-to"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { cn } from "@/lib/utils"
import { zodResolver } from "@/lib/zod-resolver"

/** 机器人凭据表单校验规则。 */
const weComBotConnectionSchema = z.object({
  botId: z.string().trim().min(1).max(256),
  secret: z.string().trim().min(1).max(256),
})

type WeComBotConnectionFormValues = z.infer<typeof weComBotConnectionSchema>

/** 编辑机器人 Bot ID 与 Secret，并展示当前连接状态。 */
export function WeComBotChannelConnectionForm({
  channel,
  onUpdated,
  onSavingChange,
}: {
  channel: WeComBotChannel
  onUpdated: (channel: WeComBotChannel) => void
  onSavingChange: (saving: boolean) => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const { leave } = useReturnTo("/channels")
  const immediateSave = useImmediateSave()
  const saving = immediateSave.saving

  const form = useForm<WeComBotConnectionFormValues>({
    resolver: zodResolver(weComBotConnectionSchema),
    shouldUseNativeValidation: true,
    defaultValues: connectionFormValues(channel),
  })

  /** 保存 Bot ID 与 Secret，持有连接的服务端随后按新凭据重连；reconnect 为真时凭据未变也重新保存，用于被其他连接接管后重连。 */
  async function save(values: WeComBotConnectionFormValues, reconnect = false) {
    const saved = connectionFormValues(channel)
    if (!reconnect && values.botId === saved.botId && values.secret === saved.secret) return
    const request = immediateSave.begin()
    if (request === null) return
    onSavingChange(true)
    try {
      const updated = await saveWeComBotChannelConnection(channel.id, values)
      if (!immediateSave.isCurrent(request)) return
      form.reset(connectionFormValues(updated))
      onUpdated(updated)
    } catch (error) {
      if (!immediateSave.isCurrent(request)) return
      if (recoverSession(error, navigate)) return
      if (isNotFoundApiError(error)) {
        console.warn("企业微信机器人渠道不存在", { channel_id: channel.id })
        leave({ replace: true })
        return
      }
      console.warn("保存企业微信机器人设置失败", { channel_id: channel.id, error })
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["botId", "secret"])
          : t("wecomBotConnection.saveError"),
      )
    } finally {
      immediateSave.finish(request)
      onSavingChange(false)
    }
  }

  const commit = () => void form.handleSubmit((values) => save(values))()
  const status = connectionStatus(channel)
  return (
    <form className="w-full space-y-6" onSubmit={form.handleSubmit((values) => save(values))} noValidate>
      <InlineEditField
        name="botId"
        control={form.control}
        label={t("wecomBotConnection.form.botId")}
        required
        autoComplete="off"
        maxLength={256}
        onCommit={commit}
      />
      <InlineEditField
        name="secret"
        control={form.control}
        label={t("wecomBotConnection.form.secret")}
        required
        autoComplete="off"
        maxLength={256}
        format={() => "•".repeat(8)}
        onCommit={commit}
      />
      <DetailEditRow
        label={t("wecomBotConnection.form.status")}
        value={
          <span className="flex items-center gap-2">
            <span
              aria-hidden
              className={cn(
                "size-2 rounded-full",
                status === "online"
                  ? "bg-success"
                  : status === "rejected" || status === "replaced"
                    ? "bg-destructive"
                    : "bg-muted-foreground/40",
              )}
            />
            {t(`wecomBotConnection.status.${status}`)}
          </span>
        }
        editing={false}
        editEnabled={false}
      >
        {null}
      </DetailEditRow>
      {status === "replaced" ? (
        <div className="space-y-3 px-2">
          <p className="text-sm text-muted-foreground">
            {t("wecomBotConnection.replacedHelp")}
          </p>
          <Button
            type="button"
            variant="outline"
            disabled={saving}
            onClick={() => void save(connectionFormValues(channel), true)}
          >
            {saving ? <LoaderCircleIcon className="animate-spin" /> : null}
            {t("wecomBotConnection.reconnect")}
          </Button>
        </div>
      ) : null}
    </form>
  )
}

/** 由渠道详情得到连接表单值。 */
function connectionFormValues(
  channel: WeComBotChannel,
): WeComBotConnectionFormValues {
  return { botId: channel.connection.botId, secret: channel.connection.secret }
}

/** 返回展示用的连接状态：渠道停用或未设置凭据时不看连接记录，暂无服务端接管时为等待连接。 */
function connectionStatus(channel: WeComBotChannel) {
  if (!channel.enabled) return "disabled"
  if (!channel.connection.botId || !channel.connection.secret) return "unconfigured"
  switch (channel.connection.state.status) {
    case ChannelConnectionStatus.ChannelConnectionConnecting:
      return "connecting"
    case ChannelConnectionStatus.ChannelConnectionOnline:
      return "online"
    case ChannelConnectionStatus.ChannelConnectionReplaced:
      return "replaced"
    case ChannelConnectionStatus.ChannelConnectionRejected:
      return "rejected"
    case ChannelConnectionStatus.ChannelConnectionOffline:
      return "offline"
    default:
      return "waiting"
  }
}

/** 判断渠道已启用并设置了凭据，连接状态随服务端连接变化，页面需要定期刷新。 */
export function weComBotConnectionWatched(channel: WeComBotChannel) {
  const status = connectionStatus(channel)
  return status !== "disabled" && status !== "unconfigured"
}
