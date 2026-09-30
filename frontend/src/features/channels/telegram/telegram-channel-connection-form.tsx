/** Telegram 机器人 Token 测试和保存表单。 */
import { useMemo, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  isApiError,
  isNotFoundApiError,
  isTelegramBotReuseConfirmationError,
  saveTelegramChannelConnection,
  testTelegramChannelConnection,
  type TelegramChannel,
} from "@/api"
import { InlineEditField } from "@/components/form/inline-edit-field"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Button } from "@/components/ui/button"
import { resolveServerURL } from "@/lib/server-url"
import {
  createTelegramChannelConnectionSchema,
  type TelegramChannelConnectionFormValues,
} from "@/features/channels/telegram/telegram-channel-connection-schema"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 编辑 Telegram 机器人连接。 */
export function TelegramChannelConnectionForm({
  channel,
  onUpdated,
  onSavingChange,
}: {
  channel: TelegramChannel
  onUpdated: (channel: TelegramChannel) => void
  onSavingChange: (saving: boolean) => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [pendingBotReuse, setPendingBotReuse] =
    useState<TelegramChannelConnectionFormValues | null>(null)
  const schema = useMemo(
    () =>
      createTelegramChannelConnectionSchema({
        tokenRequired: t("telegramConnection.validation.tokenRequired"),
        tokenTooLong: t("telegramConnection.validation.tokenTooLong"),
      }),
    [t],
  )
  const form = useForm<TelegramChannelConnectionFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { botToken: channel.connection.botToken },
  })

  /** 保存 Token、机器人信息和回调基础地址。 */
  async function save(
    values: TelegramChannelConnectionFormValues,
    confirmBotReuse = false,
  ) {
    if (saving) return
    setSaving(true)
    onSavingChange(true)
    try {
      const webhookBaseURL = await resolveServerURL()
      const updated = await saveTelegramChannelConnection(channel.id, {
        botToken: values.botToken,
        webhookBaseURL,
        confirmBotReuse,
      })
      form.reset({ botToken: updated.connection.botToken })
      onUpdated(updated)
    } catch (error) {
      if (recoverSession(error, navigate)) return
      if (isNotFoundApiError(error)) {
        console.warn("Telegram 渠道不存在", { channel_id: channel.id })
        navigate("/channels", { replace: true })
        return
      }
      if (!confirmBotReuse && isTelegramBotReuseConfirmationError(error)) {
        setPendingBotReuse({ ...values })
        return
      }
      console.warn("保存 Telegram 连接失败", {
        channel_id: channel.id,
        error,
      })
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["botToken", "webhookBaseURL"])
          : t("telegramConnection.saveError"),
      )
    } finally {
      setSaving(false)
      onSavingChange(false)
    }
  }

  /** 确认复用 Bot 后重新提交保存。 */
  function confirmBotReuse() {
    if (!pendingBotReuse) return
    const values = pendingBotReuse
    setPendingBotReuse(null)
    void save(values, true)
  }

  /** 仅通过 getMe 测试当前草稿 Token。 */
  async function test(values: TelegramChannelConnectionFormValues) {
    if (testing) return
    setTesting(true)
    try {
      await testTelegramChannelConnection(channel.id, {
        botToken: values.botToken,
      })
      toast.success(t("telegramConnection.tested"))
    } catch (error) {
      if (recoverSession(error, navigate)) return
      if (isNotFoundApiError(error)) {
        console.warn("Telegram 渠道不存在", { channel_id: channel.id })
        navigate("/channels", { replace: true })
        return
      }
      console.warn("测试 Telegram 连接失败", {
        channel_id: channel.id,
        error,
      })
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["botToken"])
          : t("telegramConnection.testError"),
      )
    } finally {
      setTesting(false)
    }
  }

  return (
    <>
      <form
        className="w-full space-y-6"
        onSubmit={form.handleSubmit((values) => save(values))}
        noValidate
      >
        <InlineEditField
          name="botToken"
          control={form.control}
          label={t("telegramConnection.form.botToken")}
          required
          autoComplete="off"
          maxLength={512}
          format={(value) => {
            // Token 只展示冒号前的机器人编号，密钥部分打码。
            const separator = value.indexOf(":")
            return separator > 0
              ? `${value.slice(0, separator)}:${"•".repeat(8)}`
              : "•".repeat(8)
          }}
          onCommit={() => void form.handleSubmit((values) => save(values))()}
        />
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={testing || saving}
            onClick={form.handleSubmit((values) => test(values))}
          >
            {testing ? <LoaderCircleIcon className="animate-spin" /> : null}
            {testing
              ? t("telegramConnection.form.testing")
              : t("telegramConnection.form.test")}
          </Button>
        </div>
      </form>
      <ConfirmationDialog
        open={pendingBotReuse !== null}
        pending={saving}
        title={t("telegramConnection.reuseConfirmation.title")}
        description={t("telegramConnection.reuseConfirmation.description")}
        destructive={false}
        onOpenChange={(open) => {
          if (!open) setPendingBotReuse(null)
        }}
        onConfirm={confirmBotReuse}
      />
    </>
  )
}
