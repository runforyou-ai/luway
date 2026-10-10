/** 客户会话输入区的翻译入口：切换发送时翻译，并在发送前预览译文与回译；桌面端使用 Popover，移动端使用底部 Sheet。 */
import { useEffect, useId, useRef, useState } from "react"
import { LanguagesIcon, LoaderCircleIcon, RefreshCwIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { isApiError, previewCustomerReplyTranslation, type CustomerReplyTranslation } from "@/api"
import { IconTooltip } from "@/components/icon-tooltip"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Sheet, SheetClose, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Switch } from "@/components/ui/switch"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDebouncedValue } from "@/hooks/use-debounced-value"
import { useResource } from "@/hooks/use-resource"
import { focusDialogContainer } from "@/lib/dialog-focus"
import { apiErrorMessage } from "@/lib/form-errors"
import { languageDisplayName } from "@/lib/languages"
import { cn } from "@/lib/utils"

import { composerAlignOffset, composerToolClass } from "./composer-tool"
import { useCustomerTranslation } from "@/features/inbox/shared/customer-translation"

/** 打开期间草稿变化后重新翻译的防抖时长（毫秒）。 */
const previewSourceDebounceDelay = 600

/** 展示翻译入口与译文预览，发送预览过的译文。 */
export function ComposerTranslation({
  draft,
  disabled,
  mobile,
  open,
  onOpenChange,
  onSend,
}: {
  draft: string
  disabled: boolean
  mobile: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
  onSend: (translation: CustomerReplyTranslation) => void
}) {
  const { t, i18n } = useTranslation(["inbox", "common"])
  const switchID = useId()
  const translation = useCustomerTranslation()
  const triggerRef = useRef<HTMLButtonElement>(null)
  const [alignOffset, setAlignOffset] = useState(0)
  const current = draft.trim()
  // 打开时立即以当前草稿翻译，打开期间草稿变化防抖后才重新翻译。
  const source = useDebouncedValue(current, previewSourceDebounceDelay, !open)

  useEffect(() => {
    if (open) setAlignOffset(composerAlignOffset(triggerRef.current))
  }, [open])

  const conversationID = translation?.conversationID ?? ""
  const translateReply = translation?.translateReply ?? false
  const customerLanguageTag = translation?.state.customerLanguage ?? ""
  const ready = open && translateReply && source !== ""
  // 预览绑定当前回复语言，回复语言变化后作废旧译文。
  const preview = useResource(
    resourceKeys.customerReplyTranslation(conversationID, {
      body: source, language: customerLanguageTag, locked: translation?.state.replyLanguageLocked ?? false,
      viewerLanguage: translation?.state.viewerLanguage ?? "",
    }),
    () => previewCustomerReplyTranslation(conversationID, { body: source }),
    { enabled: ready, staleTime: Infinity, refetchOnWindowFocus: false },
  )
  // 预览落后于当前草稿时按翻译中处理，不展示旧译文。
  const generating = ready && (source !== current || preview.loading || preview.refreshing)
  const result = ready && !generating && !preview.error ? preview.data : undefined
  if (!translation) return null
  const toggleLabel = customerLanguageTag
    ? t("translationReplyOn", { language: languageDisplayName(customerLanguageTag, i18n.language) })
    : t("translationReplyOnCustomer")
  const triggerLabel = translateReply ? toggleLabel : t("translationReplyOff")

  const trigger = (
    <Button
      ref={triggerRef}
      type="button"
      variant="ghost"
      size="icon-sm"
      className={cn(composerToolClass, translateReply && "text-primary hover:text-primary")}
      disabled={disabled}
      aria-label={t("translationReply")}
      aria-pressed={translateReply}
    >
      {open && generating ? <LoaderCircleIcon className="animate-spin" /> : <LanguagesIcon />}
    </Button>
  )

  const toggle = (
    <div className="flex items-center gap-2">
      <Switch
        id={switchID}
        checked={translateReply}
        onCheckedChange={translation.setTranslateReply}
      />
      <label htmlFor={switchID} className={cn("min-w-0 flex-1 truncate", mobile ? "text-sm" : "text-xs")}>
        {toggleLabel}
      </label>
    </div>
  )

  const body = (
    <div
      aria-live="polite"
      aria-busy={generating}
      className={cn("overflow-y-auto", mobile ? "h-56 min-h-0 px-4" : "max-h-[min(20rem,calc(100dvh-17rem))] min-h-28")}
    >
      {!translateReply ? (
        <PreviewHint mobile={mobile}>{t("translationReplyOffHint")}</PreviewHint>
      ) : current === "" ? (
        <PreviewHint mobile={mobile}>{t("translationPreviewEmpty")}</PreviewHint>
      ) : generating ? (
        <div className="flex h-28 items-center justify-center">
          <span className="sr-only">{t("translationPreviewGenerating")}</span>
          <div aria-hidden="true" className="w-28 space-y-2">
            <div className="h-2.5 w-full animate-pulse rounded bg-muted-foreground/25" />
            <div className="h-2.5 w-5/6 animate-pulse rounded bg-muted-foreground/20" />
            <div className="h-2.5 w-2/3 animate-pulse rounded bg-muted-foreground/15" />
          </div>
        </div>
      ) : preview.error ? (
        <PreviewHint mobile={mobile} error>
          {isApiError(preview.error) ? apiErrorMessage(preview.error) : t("translationPreviewError")}
        </PreviewHint>
      ) : result && result.language === "" ? (
        <PreviewHint mobile={mobile}>{t("translationPreviewSameLanguage")}</PreviewHint>
      ) : result ? (
        <div className={cn("space-y-3 leading-6", mobile ? "text-base" : "text-sm")}>
          <section className="space-y-1">
            <h3 className="text-xs font-medium text-muted-foreground">
              {languageDisplayName(result.language, i18n.language)}
            </h3>
            <p className="whitespace-pre-wrap">{result.body}</p>
          </section>
          <section className="space-y-1 border-t pt-3">
            <h3 className="text-xs font-medium text-muted-foreground">{t("translationBackTranslation")}</h3>
            <p className="whitespace-pre-wrap text-muted-foreground">{result.backTranslation}</p>
          </section>
        </div>
      ) : null}
    </div>
  )

  const sendable = Boolean(result && result.language !== "" && !disabled)
  /** 发送当前预览的译文。 */
  const sendPreview = () => {
    if (result && result.language !== "") onSend({ language: result.language, body: result.body })
  }
  const regenerate = (
    <Button
      type="button"
      variant="ghost"
      size={mobile ? "default" : "icon-sm"}
      className={mobile ? "min-h-11" : "size-7 text-muted-foreground hover:text-foreground"}
      disabled={!ready || generating}
      aria-label={t("translationPreviewRegenerate")}
      title={t("translationPreviewRegenerate")}
      onClick={() => void preview.refresh()}
    >
      <RefreshCwIcon className="size-4" />
      {mobile ? t("translationPreviewRegenerate") : null}
    </Button>
  )

  if (mobile) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent
          side="bottom"
          showCloseButton={false}
          aria-describedby={undefined}
          className="max-h-[calc(100dvh-env(safe-area-inset-top)-1rem)] gap-0 overflow-hidden rounded-t-2xl pb-[env(safe-area-inset-bottom)]"
          onOpenAutoFocus={focusDialogContainer}
        >
          <SheetHeader className="shrink-0 flex-row items-center border-b">
            <SheetTitle className="flex-1">{t("translationReply")}</SheetTitle>
            <SheetClose asChild>
              <Button variant="ghost" className="min-h-11">
                {t("common:actions.cancel")}
              </Button>
            </SheetClose>
          </SheetHeader>
          <div className="shrink-0 p-4">{toggle}</div>
          {body}
          <div className="grid shrink-0 grid-cols-2 gap-2 p-4">
            {regenerate}
            <Button type="button" className="min-h-11" disabled={!sendable} onClick={sendPreview}>
              {t("translationPreviewSend")}
            </Button>
          </div>
        </SheetContent>
      </Sheet>
    )
  }

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <IconTooltip label={triggerLabel}>
        <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      </IconTooltip>
      <PopoverContent
        side="top"
        align="end"
        alignOffset={alignOffset}
        className="w-[min(27rem,calc(100vw-2rem))] space-y-3 p-3"
      >
        {toggle}
        {body}
        {translateReply ? (
          <div className="flex items-center justify-end gap-2">
            {regenerate}
            <Button type="button" size="sm" disabled={!sendable} onClick={sendPreview}>
              {t("translationPreviewSend")}
            </Button>
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  )
}

/** 预览区的提示文字，占满预览区高度居中显示。 */
function PreviewHint({ children, mobile, error = false }: { children: string; mobile: boolean; error?: boolean }) {
  return (
    <div
      className={cn(
        "flex h-28 items-center justify-center rounded-md border border-dashed px-3 text-center text-xs text-muted-foreground",
        mobile && "h-full text-sm",
        error && "text-destructive",
      )}
    >
      {children}
    </div>
  )
}
