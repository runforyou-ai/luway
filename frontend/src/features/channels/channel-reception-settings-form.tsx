/** 消息渠道接待设置表单。 */
import { useMemo } from "react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  isNotFoundApiError,
  updateMessageChannelReception,
  type MessageChannelSummary,
} from "@/api"
import { FieldGroup } from "@/components/ui/field"
import { ChannelReceptionSettingsFields } from "@/features/channels/reception/channel-reception-settings-fields"
import {
  createChannelReceptionSchema,
  type ChannelReceptionSettingsFormValues,
} from "@/features/channels/reception/channel-reception-schema"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useReturnTo } from "@/hooks/use-return-to"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 修改消息渠道的接待设置。 */
export function ChannelReceptionSettingsForm({
  channel,
  onUpdated,
}: {
  channel: MessageChannelSummary
  onUpdated: (channel: MessageChannelSummary) => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const { leave } = useReturnTo("/channels")
  const schema = useMemo(
    () =>
      createChannelReceptionSchema({
        teamRequired: t("validation.teamRequired"),
        memberRequired: t("validation.memberRequired"),
        fallbackDifferent: t("validation.fallbackDifferent"),
      }),
    [t],
  )
  const form = useForm<ChannelReceptionSettingsFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      newConversationTarget: channel.newConversationTarget,
      fallbackTarget: channel.fallbackTarget,
    },
  })
  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save: submit })

  /** 保存消息渠道接待设置。 */
  async function submit(values: ChannelReceptionSettingsFormValues) {
    try {
      const updated = await updateMessageChannelReception(channel.id, values)
      const next = {
        newConversationTarget: updated.newConversationTarget,
        fallbackTarget: updated.fallbackTarget,
      }
      acceptSaved(values, next)
      onUpdated(updated)
      return true
    } catch (error) {
      if (recoverSession(error, navigate)) return false
      if (isNotFoundApiError(error)) {
        console.warn("消息渠道不存在", { channel_id: channel.id })
        leave({ replace: true })
        return false
      }
      console.warn("保存消息渠道接待设置失败", error)
      toast.error(requestErrorMessage(error, ["newConversationTarget", "fallbackTarget"]))
      return false
    }
  }

  return (
    <form
      className="w-full"
      onSubmit={form.handleSubmit(() => saveNow())}
      noValidate
    >
      <FieldGroup>
        <ChannelReceptionSettingsFields
          control={form.control}
          channelType={channel.type}
        />
      </FieldGroup>
    </form>
  )
}
