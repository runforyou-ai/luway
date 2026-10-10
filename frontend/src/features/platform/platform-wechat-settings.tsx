/** 部署配置的微信开放平台页签：维护第三方平台凭据，展示平台凭据状态、需要填写到微信的接入信息与各服务器出口 IP。 */
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { z } from "zod"

import {
  checkWechatPlatform,
  getWechatPlatform,
  saveWechatPlatform,
  WechatPlatformStatus,
  WechatTokenFailure,
  type WechatPlatform,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceContent } from "@/components/resource-content"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { WechatServerEgressList } from "@/components/wechat/server-egress-list"
import { resourceKeys } from "@/hooks/resource-keys"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { useDateTime } from "@/hooks/use-date-time"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { randomText } from "@/lib/random-text"
import { zodResolver } from "@/lib/zod-resolver"

/** 等待验证票据或平台凭据时刷新状态的间隔毫秒数。 */
const pendingRefreshInterval = 15000

/** 常规刷新状态的间隔毫秒数。 */
const idleRefreshInterval = 60000

/** 每次进入页签读取最新配置，等待票据或凭据期间短间隔刷新，依次渲染平台状态、平台凭据、保存后的接入信息与 IP 白名单。 */
export function PlatformWechatSettings() {
  const { t } = useTranslation("platform")
  const platform = useResource(resourceKeys.wechatPlatform(), (signal) => getWechatPlatform(signal), {
    staleTime: 0,
    refetchInterval: (data) =>
      data?.status === WechatPlatformStatus.WaitingTicket || data?.status === WechatPlatformStatus.Pending
        ? pendingRefreshInterval
        : idleRefreshInterval,
  })

  return (
    <ResourceContent resources={platform} errorMessage={t("wechat.loadError")}>
      {platform.data ? (
        <div className="w-full space-y-12">
          {platform.data.configured ? <PlatformStatus platform={platform.data} /> : null}
          <CredentialsForm platform={platform.data} />
          {platform.data.configured ? <AccessSettings platform={platform.data} /> : null}
          <ServerWhitelist platform={platform.data} />
        </div>
      ) : null}
    </ResourceContent>
  )
}

/** 带标题与说明的页面分组。 */
function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <section className="space-y-6">
      <div className="space-y-1">
        <h2 className="text-base font-medium">{title}</h2>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {children}
    </section>
  )
}

/** 展示平台可用状态、最近一次失败原因、验证票据接收时间与凭据有效期；已收到票据但凭据不可用时提供重新检测。 */
function PlatformStatus({ platform }: { platform: WechatPlatform }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const [checking, setChecking] = useState(false)
  const status = platform.status ?? WechatPlatformStatus.WaitingTicket
  const statusViews: Record<string, { label: string; variant: "success" | "warning" | "destructive" }> = {
    [WechatPlatformStatus.WaitingTicket]: { label: t("wechat.statuses.waiting_ticket"), variant: "warning" },
    [WechatPlatformStatus.Pending]: { label: t("wechat.statuses.pending"), variant: "warning" },
    [WechatPlatformStatus.Ready]: { label: t("wechat.statuses.ready"), variant: "success" },
    [WechatPlatformStatus.Failed]: { label: t("wechat.statuses.failed"), variant: "destructive" },
  }
  const view = statusViews[status]
  const checkable = status === WechatPlatformStatus.Pending || status === WechatPlatformStatus.Failed
  // 按失败原因组织说明。
  let failureText = ""
  if (platform.tokenFailure === WechatTokenFailure.IPNotWhitelisted) {
    failureText = platform.tokenFailureDetail
      ? t("wechat.failures.ip_not_whitelisted", { ip: platform.tokenFailureDetail })
      : t("wechat.failures.ip_not_whitelisted_unknown")
  } else if (platform.tokenFailure === WechatTokenFailure.Rejected) {
    failureText = t("wechat.failures.rejected", { detail: platform.tokenFailureDetail })
  } else if (platform.tokenFailure) {
    failureText = t("wechat.failures.unavailable", { detail: platform.tokenFailureDetail })
  }
  let help = ""
  if (status === WechatPlatformStatus.WaitingTicket) help = t("wechat.waitingTicketHelp")
  else if (status === WechatPlatformStatus.Pending) help = t("wechat.pendingHelp")

  /** 立即重新获取平台凭据并刷新状态。 */
  async function check() {
    setChecking(true)
    try {
      await checkWechatPlatform()
      toast.success(t("wechat.checked"))
    } catch (error) {
      reportError(error, { log: "检测微信平台凭据", fallback: t("wechat.checkError") })
    } finally {
      setChecking(false)
      void invalidate(resourceKeys.wechatPlatform())
    }
  }

  return (
    <Section title={t("wechat.statusTitle")}>
      <FieldGroup>
        <Field>
          <FieldLabel>{t("wechat.status")}</FieldLabel>
          <div className="flex min-h-11 flex-wrap items-center justify-between gap-3">
            <StatusBadge variant={view.variant}>{view.label}</StatusBadge>
            {checkable ? (
              <Button type="button" variant="outline" className="touch:min-h-11" disabled={checking} onClick={() => void check()}>
                {checking ? <LoaderCircleIcon className="animate-spin" /> : null}
                {checking ? t("wechat.checking") : t("wechat.check")}
              </Button>
            ) : null}
          </div>
          {failureText ? <FieldDescription className="text-destructive">{failureText}</FieldDescription> : null}
          {help ? <FieldDescription>{help}</FieldDescription> : null}
        </Field>
        <div className="grid gap-6 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="wechat-ticket-received-at">{t("wechat.ticketReceivedAt")}</FieldLabel>
            <Input
              id="wechat-ticket-received-at"
              value={platform.verifyTicketReceivedAt ? formatDateTime(platform.verifyTicketReceivedAt) : t("wechat.ticketNever")}
              readOnly
              className="text-muted-foreground"
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="wechat-token-expires-at">{t("wechat.tokenExpiresAt")}</FieldLabel>
            <Input
              id="wechat-token-expires-at"
              value={platform.accessTokenExpiresAt ? formatDateTime(platform.accessTokenExpiresAt) : "—"}
              readOnly
              className="text-muted-foreground"
            />
          </Field>
        </div>
      </FieldGroup>
    </Section>
  )
}

/** 编辑第三方平台凭据；已配置时更换 Component AppID 需先确认影响，保存后刷新状态。 */
function CredentialsForm({ platform }: { platform: WechatPlatform }) {
  const { t } = useTranslation(["platform", "wechat", "common"])
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const submitButtonRef = useRef<HTMLButtonElement>(null)
  const schema = useMemo(
    () =>
      z.object({
        componentAppId: z.string().trim().regex(/^wx[0-9a-f]{16}$/, t("wechat.componentAppIdInvalid")),
        componentAppSecret: z.string().trim().min(1),
        token: z.string().trim().regex(/^[A-Za-z0-9]{3,32}$/, t("wechat.tokenInvalid")),
        encodingAesKey: z.string().trim().regex(/^[A-Za-z0-9]{43}$/, t("wechat.encodingAesKeyInvalid")),
      }),
    [t],
  )
  const [replacing, setReplacing] = useState(false)
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      componentAppId: platform.componentAppId,
      componentAppSecret: platform.componentAppSecret,
      token: platform.token,
      encodingAesKey: platform.encodingAesKey,
    },
  })
  useFormLifetime(form.formState.isDirty)
  const { isDirty, isSubmitting } = form.formState

  // 未修改的表单跟随服务端最新配置，其他客户端保存后不保留旧值。
  useEffect(() => {
    if (isDirty || isSubmitting) return
    form.reset({
      componentAppId: platform.componentAppId,
      componentAppSecret: platform.componentAppSecret,
      token: platform.token,
      encodingAesKey: platform.encodingAesKey,
    })
  }, [form, isDirty, isSubmitting, platform.componentAppId, platform.componentAppSecret, platform.token, platform.encodingAesKey])

  /** 保存凭据并以保存结果重置表单。 */
  async function save(values: z.infer<typeof schema>) {
    try {
      const saved = await saveWechatPlatform(values)
      form.reset({
        componentAppId: saved.componentAppId,
        componentAppSecret: saved.componentAppSecret,
        token: saved.token,
        encodingAesKey: saved.encodingAesKey,
      })
      setReplacing(false)
      toast.success(t("wechat.saved"))
      void invalidate(resourceKeys.wechatPlatform())
    } catch (error) {
      reportError(error, {
        log: "保存微信开放平台配置",
        fallback: t("wechat.saveError"),
        fields: ["componentAppId", "componentAppSecret", "token", "encodingAesKey"],
      })
    }
  }

  /** 更换已配置的 Component AppID 时先确认，其余情况直接保存。 */
  function submit(values: z.infer<typeof schema>) {
    if (platform.configured && values.componentAppId !== platform.componentAppId) {
      setReplacing(true)
      return
    }
    return save(values)
  }

  /** 渲染一个凭据输入框，给出 generateLength 时附带生成该长度随机值的按钮。 */
  function credentialField(name: keyof z.infer<typeof schema>, label: string, generateLength?: number) {
    return (
      <Controller
        name={name}
        control={form.control}
        render={({ field, fieldState }) => (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor={`wechat-${name}`} required>
              {label}
            </FieldLabel>
            <div className="flex items-center gap-2">
              <Input
                {...field}
                id={`wechat-${name}`}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                aria-invalid={fieldState.invalid}
                required
              />
              {generateLength ? (
                <Button
                  type="button"
                  variant="outline"
                  className="h-11 shrink-0"
                  onClick={() => form.setValue(name, randomText(generateLength), { shouldDirty: true, shouldValidate: true })}
                >
                  {t("wechat:generate")}
                </Button>
              ) : null}
            </div>
          </Field>
        )}
      />
    )
  }

  return (
    <Section title={t("wechat.credentialsTitle")} description={t("wechat.credentialsHelp")}>
      <form className="w-full space-y-9" aria-label={t("wechat.credentialsTitle")} onSubmit={form.handleSubmit(submit)} noValidate>
        <FieldGroup>
          <div className="grid gap-6 sm:grid-cols-2">
            {credentialField("componentAppId", t("wechat.componentAppId"))}
            {credentialField("componentAppSecret", t("wechat.componentAppSecret"))}
          </div>
          {credentialField("token", t("wechat.token"), 32)}
          {credentialField("encodingAesKey", t("wechat.encodingAesKey"), 43)}
        </FieldGroup>
        <div className="flex justify-end">
          <Button ref={submitButtonRef} type="submit" className="touch:min-h-11 touch:flex-1" disabled={isSubmitting}>
            {isSubmitting && !replacing ? <LoaderCircleIcon className="animate-spin" /> : null}
            {isSubmitting && !replacing ? t("common:actions.saving") : t("common:actions.save")}
          </Button>
        </div>
      </form>
      <ConfirmationDialog
        open={replacing}
        pending={isSubmitting}
        title={t("wechat.replaceTitle")}
        description={t("wechat.replaceDescription")}
        onOpenChange={setReplacing}
        onConfirm={() => void form.handleSubmit(save)()}
        onCloseAutoFocus={(event) => {
          // 关闭确认框后焦点回到保存按钮。
          event.preventDefault()
          submitButtonRef.current?.focus()
        }}
      />
    </Section>
  )
}

/** 只读展示需要填写到微信开放平台的授权发起页域名与授权事件接收地址，各自提供复制。 */
function AccessSettings({ platform }: { platform: WechatPlatform }) {
  const { t } = useTranslation(["platform", "wechat", "common"])
  const { copied, copy } = useCopyFeedback<"domain" | "event" | "message">()
  const items = [
    { key: "domain", label: t("wechat.authorizationDomain"), value: platform.authorizationDomain },
    { key: "event", label: t("wechat.eventUrl"), value: platform.eventUrl },
    { key: "message", label: t("wechat.messageUrl"), value: platform.messageUrl },
  ] as const

  return (
    <Section title={t("wechat.accessTitle")} description={t("wechat.accessHelp")}>
      <FieldGroup>
        {items.map((item) => (
          <Field key={item.key}>
            <FieldLabel htmlFor={`wechat-access-${item.key}`}>{item.label}</FieldLabel>
            <div className="flex items-center gap-2">
              <Input id={`wechat-access-${item.key}`} value={item.value} readOnly className="font-mono text-muted-foreground" />
              <Button
                type="button"
                variant="outline"
                className="h-11 shrink-0"
                onClick={() => void copy(item.value, item.key).then((done) => done || toast.error(t("wechat:copyError")))}
              >
                {copied === item.key ? t("common:actions.copied") : t("common:actions.copy")}
              </Button>
            </div>
          </Field>
        ))}
      </FieldGroup>
    </Section>
  )
}

/** IP 白名单分组：列出各服务器的出口 IP。 */
function ServerWhitelist({ platform }: { platform: WechatPlatform }) {
  const { t } = useTranslation("platform")

  return (
    <Section title={t("wechat.whitelistTitle")} description={t("wechat.whitelistHelp")}>
      <WechatServerEgressList servers={platform.servers} />
    </Section>
  )
}
