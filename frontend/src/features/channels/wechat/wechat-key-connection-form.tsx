/** 密钥接入公众号渠道的接入状态与凭据表单。 */
import { useEffect, useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { z } from "zod"

import {
  checkWechatKeyChannelConnection,
  saveWechatKeyChannelConnection,
  WechatEncryptionMode,
  type WechatKeyChannel,
} from "@/api"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { WechatCredentialStatus } from "@/features/channels/wechat/wechat-credential-status"
import { WechatKeyServerConfig } from "@/features/channels/wechat/wechat-key-server-config"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { randomText } from "@/lib/random-text"
import { zodResolver } from "@/lib/zod-resolver"

/** 编辑密钥接入连接：已连接时先展示接口凭据状态与服务器配置，再展示凭据表单。 */
export function WechatKeyConnectionForm({
  channel,
  onUpdated,
}: {
  channel: WechatKeyChannel
  onUpdated: () => void
}) {
  return (
    <div className="w-full space-y-12">
      {channel.connection.appId ? <ConnectionStatus channel={channel} onUpdated={onUpdated} /> : null}
      {channel.connection.appId ? <WechatKeyServerConfig channel={channel} /> : null}
      <KeyCredentialsForm channel={channel} onUpdated={onUpdated} />
    </div>
  )
}

/** 展示接口凭据状态并提供重新检测。 */
function ConnectionStatus({ channel, onUpdated }: { channel: WechatKeyChannel; onUpdated: () => void }) {
  const { t } = useTranslation("channels")

  return (
    <section className="space-y-6">
      <h2 className="text-base font-medium">{t("wechatConnection.statusTitle")}</h2>
      <FieldGroup>
        <WechatCredentialStatus
          channelId={channel.id}
          state={channel.connection}
          check={checkWechatKeyChannelConnection}
          onUpdated={onUpdated}
        />
      </FieldGroup>
    </section>
  )
}

/** 密钥接入可选的消息加解密方式。 */
type KeyEncryptionMode = typeof WechatEncryptionMode.WechatEncryptionSafe | typeof WechatEncryptionMode.WechatEncryptionPlain

/** 由渠道连接得到表单值，尚未连接时消息加解密方式默认安全模式。 */
function keyFormValues(channel: WechatKeyChannel): {
  appId: string
  appSecret: string
  token: string
  encryptionMode: KeyEncryptionMode
  encodingAesKey: string
} {
  const { connection } = channel
  return {
    appId: connection.appId,
    appSecret: connection.appSecret,
    token: connection.token,
    encryptionMode:
      connection.encryptionMode === WechatEncryptionMode.WechatEncryptionPlain
        ? WechatEncryptionMode.WechatEncryptionPlain
        : WechatEncryptionMode.WechatEncryptionSafe,
    encodingAesKey: connection.encodingAesKey,
  }
}

/** 编辑密钥接入凭据，保存后刷新渠道详情。 */
function KeyCredentialsForm({ channel, onUpdated }: { channel: WechatKeyChannel; onUpdated: () => void }) {
  const { t } = useTranslation(["channels", "wechat", "common"])
  const reportError = useRequestErrorReporter()
  const connected = channel.connection.appId !== ""
  const schema = useMemo(
    () =>
      z
        .object({
          appId: z.string().trim().regex(/^wx[0-9a-f]{16}$/, t("wechatConnection.validation.appIdInvalid")),
          appSecret: z.string().trim().min(1),
          token: z.string().trim().regex(/^[A-Za-z0-9]{3,32}$/, t("wechatConnection.validation.tokenInvalid")),
          encryptionMode: z.enum([WechatEncryptionMode.WechatEncryptionSafe, WechatEncryptionMode.WechatEncryptionPlain]),
          encodingAesKey: z.string().trim(),
        })
        .superRefine((values, context) => {
          // 安全模式要求 43 位 EncodingAESKey。
          if (values.encryptionMode === WechatEncryptionMode.WechatEncryptionSafe && !/^[A-Za-z0-9]{43}$/.test(values.encodingAesKey)) {
            context.addIssue({ code: "custom", path: ["encodingAesKey"], message: t("wechatConnection.validation.encodingAesKeyInvalid") })
          }
        }),
    [t],
  )
  type FormValues = z.infer<typeof schema>
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: keyFormValues(channel),
  })
  useFormLifetime(form.formState.isDirty)
  const { isDirty, isSubmitting } = form.formState
  const encryptionMode = useWatch({ control: form.control, name: "encryptionMode" })
  const safe = encryptionMode === WechatEncryptionMode.WechatEncryptionSafe

  // 未修改的表单跟随服务端最新连接，其他客户端保存后不保留旧值。
  useEffect(() => {
    if (isDirty || isSubmitting) return
    form.reset(keyFormValues(channel))
  }, [channel, form, isDirty, isSubmitting])

  /** 保存凭据并以保存结果重置表单。 */
  async function save(values: FormValues) {
    try {
      const saved = await saveWechatKeyChannelConnection(channel.id, values)
      form.reset(keyFormValues(saved))
      toast.success(t("wechatConnection.saved"))
      onUpdated()
    } catch (error) {
      reportError(error, {
        log: "保存公众号连接",
        context: { channel_id: channel.id },
        fallback: t("wechatConnection.saveError"),
        fields: ["appId", "appSecret", "token", "encryptionMode", "encodingAesKey"],
      })
    }
  }

  /** 渲染一个凭据输入框，给出 generateLength 时附带生成该长度随机值的按钮。 */
  function credentialField(name: "appId" | "appSecret" | "token" | "encodingAesKey", label: string, options: { generateLength?: number; readOnly?: boolean; help?: string } = {}) {
    return (
      <Controller
        name={name}
        control={form.control}
        render={({ field, fieldState }) => (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor={`wechat-channel-${name}`} required={!options.readOnly}>
              {label}
            </FieldLabel>
            <div className="flex items-center gap-2">
              <Input
                {...field}
                id={`wechat-channel-${name}`}
                spellCheck={false}
                autoComplete="off"
                className={options.readOnly ? "font-mono text-muted-foreground" : "font-mono"}
                aria-invalid={fieldState.invalid}
                readOnly={options.readOnly}
                required={!options.readOnly}
              />
              {options.generateLength ? (
                <Button
                  type="button"
                  variant="outline"
                  className="h-11 shrink-0"
                  onClick={() => form.setValue(name, randomText(options.generateLength ?? 0), { shouldDirty: true, shouldValidate: true })}
                >
                  {t("wechat:generate")}
                </Button>
              ) : null}
            </div>
            {options.help ? <FieldDescription>{options.help}</FieldDescription> : null}
          </Field>
        )}
      />
    )
  }

  return (
    <section className="space-y-6">
      <div className="space-y-1">
        <h2 className="text-base font-medium">{t("wechatConnection.credentialsTitle")}</h2>
        <p className="text-sm text-muted-foreground">{t("wechatConnection.credentialsHelp")}</p>
      </div>
      <form className="w-full space-y-9" aria-label={t("wechatConnection.credentialsTitle")} onSubmit={form.handleSubmit(save)} noValidate>
        <FieldGroup>
          <div className="grid gap-6 sm:grid-cols-2">
            {credentialField("appId", t("wechatConnection.form.appId"), { readOnly: connected, help: connected ? t("wechatConnection.form.appIdHelp") : undefined })}
            {credentialField("appSecret", t("wechatConnection.form.appSecret"))}
          </div>
          {credentialField("token", t("wechatConnection.form.token"), { generateLength: 32 })}
          <Controller
            name="encryptionMode"
            control={form.control}
            render={({ field }) => (
              <Field>
                <FieldLabel htmlFor="wechat-channel-encryptionMode" required>
                  {t("wechatConnection.form.encryptionMode")}
                </FieldLabel>
                <NativeSelect id="wechat-channel-encryptionMode" name={field.name} value={field.value} onChange={(event) => field.onChange(event.target.value)}>
                  <option value={WechatEncryptionMode.WechatEncryptionSafe}>{t("wechatConnection.encryption.safe")}</option>
                  <option value={WechatEncryptionMode.WechatEncryptionPlain}>{t("wechatConnection.encryption.plain")}</option>
                </NativeSelect>
              </Field>
            )}
          />
          {safe ? credentialField("encodingAesKey", t("wechatConnection.form.encodingAesKey"), { generateLength: 43 }) : null}
        </FieldGroup>
        <div className="flex justify-end">
          <Button type="submit" className="touch:min-h-11 touch:flex-1" disabled={isSubmitting}>
            {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
            {connected
              ? isSubmitting
                ? t("common:actions.saving")
                : t("common:actions.save")
              : isSubmitting
                ? t("wechatConnection.connecting")
                : t("wechatConnection.connect")}
          </Button>
        </div>
      </form>
    </section>
  )
}
