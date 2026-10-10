/** 客户会话输入区的 AI 写回复弹层，桌面端使用 Popover，移动端使用底部 Sheet。 */
import { useEffect, useId, useRef, useState } from "react"
import { LoaderCircleIcon, RefreshCwIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ServiceReplyMode } from "@/api"
import { IconTooltip } from "@/components/icon-tooltip"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import { composerAlignOffset, composerToolClass } from "@/features/inbox/composer/composer-tool"
import {
  ReplyAgentSelect,
  ReplyCandidateList,
  ReplyModeSelector,
  ReplyToneSelect,
  useReplyAssistantPreferences,
} from "@/features/inbox/composer/customer-reply-assistant-parts"
import { useReplySuggestions } from "@/features/inbox/composer/use-reply-suggestions"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceRemover } from "@/hooks/use-resource"
import { focusDialogContainer } from "@/lib/dialog-focus"

/** 按会话上下文生成对客回复候选，使用候选后替换当前草稿。 */
export function CustomerReplyAssistant({
  conversationID,
  currentIdentityID,
  draft,
  replyToMessageID,
  disabled,
  mobile = false,
  onApply,
}: {
  conversationID: string
  currentIdentityID: string
  draft: string
  replyToMessageID: string
  disabled: boolean
  mobile?: boolean
  onApply: (reply: string) => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const fieldPrefix = useId()
  const removeResource = useResourceRemover()
  const appliedRef = useRef(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const [alignOffset, setAlignOffset] = useState(0)
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<ServiceReplyMode>(ServiceReplyMode.Reply)
  const [preferences, updatePreferences] = useReplyAssistantPreferences(currentIdentityID)
  // 不可使用时关闭弹层，恢复可用后保持关闭。
  if (disabled && open) setOpen(false)

  useEffect(
    () => () =>
      void removeResource(resourceKeys.serviceReplySuggestions(conversationID)),
    [conversationID, removeResource],
  )

  const {
    agents,
    agentIdentityID,
    ready,
    generating,
    candidates,
    emptyMessage,
    failed,
    refresh,
  } = useReplySuggestions({
    conversationID,
    open,
    disabled,
    mode,
    tone: preferences.tone,
    preferredAgentID: preferences.agentIdentityId,
    draft,
    replyToMessageID,
  })

  /** 打开弹层时按草稿是否为空选中改写或写回复，并使弹层右边缘对齐主消息区右边界。 */
  function changeOpen(nextOpen: boolean) {
    if (nextOpen) {
      setMode(
        draft.trim()
          ? ServiceReplyMode.Rewrite
          : ServiceReplyMode.Reply,
      )
      setAlignOffset(composerAlignOffset(triggerRef.current))
    }
    setOpen(nextOpen)
  }

  /** 以候选替换当前对客草稿并关闭弹层。 */
  function applyCandidate(candidate: string) {
    appliedRef.current = true
    onApply(candidate)
    setOpen(false)
    void removeResource(resourceKeys.serviceReplySuggestions(conversationID))
  }

  /** 使用候选后焦点交给回复输入框。 */
  function keepAppliedFocus(event: Event) {
    if (!appliedRef.current) return
    appliedRef.current = false
    event.preventDefault()
  }

  const trigger = (
    <Button
      ref={triggerRef}
      type="button"
      variant="ghost"
      size="icon-sm"
      className={composerToolClass}
      disabled={disabled}
      aria-label={t("replyAssistant")}
    >
      {open && generating ? (
        <LoaderCircleIcon className="animate-spin" />
      ) : (
        <span aria-hidden="true" className="text-[15px] leading-none font-medium tracking-tight">
          AI
        </span>
      )}
    </Button>
  )

  const modeSelector = <ReplyModeSelector mode={mode} mobile={mobile} onChange={setMode} />
  const agentSelect = (
    <ReplyAgentSelect
      id={`${fieldPrefix}-agent`}
      agents={agents}
      value={agentIdentityID}
      mobile={mobile}
      onChange={(value) => updatePreferences({ agentIdentityId: value })}
    />
  )
  const toneSelect = (
    <ReplyToneSelect
      id={`${fieldPrefix}-tone`}
      value={preferences.tone}
      mobile={mobile}
      onChange={(value) => updatePreferences({ tone: value })}
    />
  )
  const results = (
    <ReplyCandidateList
      generating={generating}
      candidates={candidates}
      emptyMessage={emptyMessage}
      failed={failed}
      mobile={mobile}
      onApply={applyCandidate}
    />
  )

  if (mobile) {
    return (
      <Sheet open={open} onOpenChange={changeOpen}>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent
          side="bottom"
          showCloseButton={false}
          aria-describedby={undefined}
          className="max-h-[calc(100dvh-env(safe-area-inset-top)-1rem)] gap-0 overflow-hidden rounded-t-2xl pb-[env(safe-area-inset-bottom)]"
          onOpenAutoFocus={focusDialogContainer}
          onCloseAutoFocus={keepAppliedFocus}
        >
          <SheetHeader className="shrink-0 flex-row items-center border-b">
            <SheetTitle className="flex-1">{t("replyAssistant")}</SheetTitle>
            <SheetClose asChild>
              <Button variant="ghost" className="min-h-11">
                {t("common:actions.cancel")}
              </Button>
            </SheetClose>
          </SheetHeader>
          <div className="shrink-0 space-y-3 p-4">
            {modeSelector}
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1.5">
                <label
                  htmlFor={`${fieldPrefix}-agent`}
                  className="block text-xs font-medium text-muted-foreground"
                >
                  {t("replyAssistantAgent")}
                </label>
                {agentSelect}
              </div>
              <div className="space-y-1.5">
                <label
                  htmlFor={`${fieldPrefix}-tone`}
                  className="block text-xs font-medium text-muted-foreground"
                >
                  {t("replyAssistantTone")}
                </label>
                {toneSelect}
              </div>
            </div>
          </div>
          {results}
          <div className="shrink-0 p-4">
            <Button
              type="button"
              variant="outline"
              className="min-h-11 w-full"
              disabled={!ready || generating}
              onClick={() => void refresh()}
            >
              <RefreshCwIcon className="size-4" />
              {t("replyAssistantRegenerate")}
            </Button>
          </div>
        </SheetContent>
      </Sheet>
    )
  }

  return (
    <Popover open={open} onOpenChange={changeOpen}>
      <IconTooltip label={t("replyAssistant")}>
        <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      </IconTooltip>
      <PopoverContent
        side="top"
        align="end"
        alignOffset={alignOffset}
        className="w-[min(27rem,calc(100vw-2rem))] space-y-3 p-3"
        onCloseAutoFocus={keepAppliedFocus}
      >
        <div className="flex items-center gap-2">
          <p className="min-w-0 truncate text-sm font-medium">{t("replyAssistant")}</p>
          {modeSelector}
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {agentSelect}
            {toneSelect}
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="size-7 text-muted-foreground hover:text-foreground"
              disabled={!ready || generating}
              aria-label={t("replyAssistantRegenerate")}
              title={t("replyAssistantRegenerate")}
              onClick={() => void refresh()}
            >
              <RefreshCwIcon className="size-4" />
            </Button>
          </div>
        </div>
        {results}
      </PopoverContent>
    </Popover>
  )
}
