/** 网站渠道接入方式页签。 */
import { useEffect, useId, useMemo, useState } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  isNotFoundApiError,
  updateWebsiteChannelAccess,
  type WebsiteChannelAccessData,
  type WebsiteChannelData,
} from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useQRCode } from "@/hooks/use-qr-code"
import { useResource } from "@/hooks/use-resource"
import { useReturnTo } from "@/hooks/use-return-to"
import { recoverSession } from "@/lib/session-navigation"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import {
  websiteChannelChatURL,
  websiteChannelWidgetSnippet,
} from "@/features/channels/website/website-channel-access"
import {
  allowedHostLines,
  createWebsiteChannelAccessSchema,
  type WebsiteChannelAccessFormValues,
} from "@/features/channels/website/website-channel-access-schema"
import { embedSDKNames, useBrand } from "@/lib/brand"
import { requestErrorMessage } from "@/lib/form-errors"
import { resolveServerURL } from "@/lib/server-url"
import { openExternalURL } from "@/platform/external-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 网站渠道接入方式子页签。 */
export type WebsiteChannelAccessTab = "embed" | "link"

/** 展示安装代码或聊天链接的使用说明。 */
function WebsiteChannelUsageInstructions({
  kind,
  snippet,
  chatUrl,
  channelId,
  onOpenChange,
}: {
  kind: WebsiteChannelAccessTab | ""
  snippet: string
  chatUrl: string
  channelId: string
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation("channels")
  const { openAttribute } = embedSDKNames(useBrand())
  const customButton = `<button type="button" ${openAttribute}="${channelId}">${t("usage.instructions.contactButton")}</button>`

  return (
    <Dialog open={kind !== ""} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        {kind === "embed" ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("usage.instructions.embedTitle")}</DialogTitle>
              <DialogDescription>
                {t("usage.instructions.embedDescription")}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-5 text-sm">
              <section className="space-y-2">
                <p className="font-medium">
                  {t("usage.instructions.addCode")}
                </p>
                <p className="text-muted-foreground">
                  {t("usage.instructions.addCodeHelp")}
                </p>
                <pre className="rounded-md border bg-muted/30 p-3 break-all whitespace-pre-wrap">
                  {snippet}
                </pre>
              </section>
              <section className="space-y-2">
                <p className="font-medium">
                  {t("usage.instructions.customButton")}
                </p>
                <p className="text-muted-foreground">
                  {t("usage.instructions.customButtonHelp")}
                </p>
                <pre className="rounded-md border bg-muted/30 p-3 break-all whitespace-pre-wrap">
                  {customButton}
                </pre>
              </section>
              <section className="space-y-2">
                <p className="font-medium">
                  {t("usage.instructions.verifyEmbed")}
                </p>
                <p className="text-muted-foreground">
                  {t("usage.instructions.verifyEmbedHelp")}
                </p>
              </section>
            </div>
          </>
        ) : kind === "link" ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("usage.instructions.linkTitle")}</DialogTitle>
              <DialogDescription>
                {t("usage.instructions.linkDescription")}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-5 text-sm">
              <section className="space-y-2">
                <p className="font-medium">
                  {t("usage.instructions.shareLink")}
                </p>
                <p className="text-muted-foreground">
                  {t("usage.instructions.shareLinkHelp")}
                </p>
                <pre className="rounded-md border bg-muted/30 p-3 break-all whitespace-pre-wrap">
                  {chatUrl}
                </pre>
              </section>
              <section className="space-y-2">
                <p className="font-medium">
                  {t("usage.instructions.useQrCode")}
                </p>
                <p className="text-muted-foreground">
                  {t("usage.instructions.useQrCodeHelp")}
                </p>
              </section>
            </div>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

/** 展示网站渠道的嵌入代码和独立访问链接。 */
export function WebsiteChannelUsagePanel({
  channel,
  access,
  onAccessChange,
  onUpdated,
}: {
  channel: WebsiteChannelData
  access: WebsiteChannelAccessTab
  onAccessChange: (value: WebsiteChannelAccessTab) => void
  onUpdated: (value: WebsiteChannelAccessData) => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const { leave } = useReturnTo("/channels")
  const formId = useId()
  const { copied, copy } = useCopyFeedback<"snippet" | "link">()
  const [copyFailed, setCopyFailed] = useState(false)
  const [instructions, setInstructions] = useState<WebsiteChannelAccessTab | "">("")
  const schema = useMemo(
    () =>
      createWebsiteChannelAccessSchema({
        tooMany: t("usage.validation.allowedHostsTooMany"),
        invalid: t("usage.validation.allowedHostInvalid"),
      }),
    [t],
  )
  const form = useForm<WebsiteChannelAccessFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      allowedHosts: channel.access.allowedHosts.join("\n"),
    },
  })
  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save })
  const originResource = useResource(resourceKeys.serverURL(), () => resolveServerURL())
  const origin = originResource.data ?? ""
  const error =
    originResource.error || originResource.data === ""
      ? t("usage.originError")
      : ""

  /** 记录访问地址读取失败或为空的原因。 */
  useEffect(() => {
    if (originResource.error) {
      console.warn("网站渠道访问地址解析失败", originResource.error)
    } else if (originResource.data === "") {
      console.warn("网站渠道访问地址为空")
    }
  }, [originResource.data, originResource.error])

  const snippet = origin
    ? websiteChannelWidgetSnippet(origin, channel.id)
    : ""
  const chatUrl = origin ? websiteChannelChatURL(origin, channel.id) : ""

  const qrCode = useQRCode(chatUrl)

  /** 复制渠道使用内容，失败时在页面上提示。 */
  async function copyUsage(value: string, target: "snippet" | "link") {
    setCopyFailed(!(await copy(value, target)))
  }

  /** 保存允许使用的网站，返回是否保存成功。 */
  async function save(values: WebsiteChannelAccessFormValues) {
    try {
      const updated = await updateWebsiteChannelAccess(channel.id, {
        allowedHosts: allowedHostLines(values.allowedHosts),
      })
      const next = { allowedHosts: updated.allowedHosts.join("\n") }
      acceptSaved(values, next)
      onUpdated(updated)
      return true
    } catch (submitError) {
      if (recoverSession(submitError, navigate)) return false
      if (isNotFoundApiError(submitError)) {
        console.warn("网站渠道不存在", { channel_id: channel.id })
        leave({ replace: true })
        return false
      }
      console.warn("保存网站渠道允许使用的网站失败", submitError)
      toast.error(
        requestErrorMessage(submitError, ["allowedHosts"]),
      )
      return false
    }
  }

  if (!origin && !error) {
    return (
      <LoadingIndicator className="py-6">
        {t("common:status.loading")}
      </LoadingIndicator>
    )
  }

  if (error) {
    return <p className="py-6 text-sm text-muted-foreground">{error}</p>
  }

  return (
    <>
      <Tabs
        value={access}
        onValueChange={(value) =>
          onAccessChange(value as WebsiteChannelAccessTab)
        }
      >
        <TabsList>
          <TabsTrigger value="embed">{t("usage.embed")}</TabsTrigger>
          <TabsTrigger value="link">{t("usage.link")}</TabsTrigger>
        </TabsList>
        <TabsContent
          value="embed"
          forceMount
          className="data-[state=inactive]:hidden"
        >
          <div className="mt-6 space-y-8">
            <FieldGroup>
              <Field>
                <div className="flex items-center gap-2">
                  <FieldLabel>{t("usage.snippet")}</FieldLabel>
                  <Button
                    type="button"
                    variant="link"
                    size="xs"
                    className="h-auto px-0 py-0 text-xs font-normal text-muted-foreground"
                    onClick={() => setInstructions("embed")}
                  >
                    {t("usage.instructions.open")}
                  </Button>
                </div>
                <FieldDescription>{t("usage.snippetHelp")}</FieldDescription>
                <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2">
                  <code className="flex min-h-8 min-w-0 flex-1 items-center font-mono text-sm break-all whitespace-pre-wrap">
                    {snippet}
                  </code>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="shrink-0"
                    onClick={() => void copyUsage(snippet, "snippet")}
                  >
                    {copied === "snippet" ? t("common:actions.copied") : t("common:actions.copy")}
                  </Button>
                </div>
              </Field>
            </FieldGroup>

            <form onSubmit={form.handleSubmit(() => saveNow())} noValidate>
              <FieldGroup>
                <Controller
                  name="allowedHosts"
                  control={form.control}
                  render={({ field, fieldState }) => (
                    <Field data-invalid={fieldState.invalid}>
                      <FieldLabel htmlFor={`${formId}-${field.name}`}>
                        {t("usage.allowedHosts")}
                      </FieldLabel>
                      <FieldDescription>
                        {t("usage.allowedHostsHelp")}
                      </FieldDescription>
                      <Textarea
                        {...field}
                        id={`${formId}-${field.name}`}
                        rows={4}
                        aria-invalid={fieldState.invalid}
                      />
                    </Field>
                  )}
                />
              </FieldGroup>
            </form>
          </div>
        </TabsContent>
        <TabsContent
          value="link"
          forceMount
          className="data-[state=inactive]:hidden"
        >
          <FieldGroup className="mt-6 gap-8">
            <Field>
              <div className="flex items-center gap-2">
                <FieldLabel>{t("usage.chatUrl")}</FieldLabel>
                <Button
                  type="button"
                  variant="link"
                  size="xs"
                  className="h-auto px-0 py-0 text-xs font-normal text-muted-foreground"
                  onClick={() => setInstructions("link")}
                >
                  {t("usage.instructions.open")}
                </Button>
              </div>
              <FieldDescription>{t("usage.chatUrlHelp")}</FieldDescription>
              <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2">
                <button
                  type="button"
                  className="flex min-h-8 min-w-0 flex-1 items-center text-left font-mono text-sm break-all hover:underline"
                  onClick={() => void openExternalURL(chatUrl)}
                >
                  {chatUrl}
                </button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={() => void copyUsage(chatUrl, "link")}
                >
                  {copied === "link" ? t("common:actions.copied") : t("common:actions.copy")}
                </Button>
              </div>
            </Field>

            <Field>
              <FieldLabel>{t("usage.qrCode")}</FieldLabel>
              <FieldDescription>{t("usage.qrCodeHelp")}</FieldDescription>
              <div>
                <div className="flex aspect-square w-40 items-center justify-center rounded-md border bg-white p-2">
                  {qrCode.dataURL ? (
                    <img
                      src={qrCode.dataURL}
                      alt={t("usage.qrCodeAlt")}
                      className="aspect-square w-full object-contain"
                    />
                  ) : (
                    <div className="space-y-2 px-3 text-center text-xs text-muted-foreground">
                      <p>
                        {qrCode.failed
                          ? t("usage.qrCodeFailed")
                          : t("usage.qrCodeLoading")}
                      </p>
                      {qrCode.failed ? (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={qrCode.retry}
                        >
                          {t("common:actions.retry")}
                        </Button>
                      ) : null}
                    </div>
                  )}
                </div>
              </div>
            </Field>
          </FieldGroup>
        </TabsContent>
        {copyFailed ? (
          <p className="mt-3 text-sm text-destructive">
            {t("usage.copyFailed")}
          </p>
        ) : null}
      </Tabs>

      <WebsiteChannelUsageInstructions
        kind={instructions}
        snippet={snippet}
        chatUrl={chatUrl}
        channelId={channel.id}
        onOpenChange={(open) => {
          if (!open) {
            setInstructions("")
          }
        }}
      />
    </>
  )
}
