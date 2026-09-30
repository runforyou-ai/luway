/** 移动端客户会话的 AI 助手子页：切换和新建 Copilot 线程、提问，并把 AI 回复填入客户会话草稿后返回。 */
import { useState } from "react"
import { CheckIcon, ChevronDownIcon, SquarePenIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useOutletContext } from "react-router"

import type { MobileCustomerConversationContext } from "@/apps/mobile/mobile-customer-conversation-page"
import { useMobileBack } from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader } from "@/apps/mobile/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import {
  CopilotAgentSelect,
  CopilotApplyReplyDialog,
  CopilotThreadsLoadState,
  CopilotThreadView,
  useCopilotThreadMeta,
  useServiceCopilot,
  type ServiceCopilot,
} from "@/features/inbox/customer-copilot-panel"
import { focusDialogContainer } from "@/lib/dialog-focus"
import { cn } from "@/lib/utils"

/** 全屏展示 AI 助手，填入回复后回到客户会话。 */
export function MobileServiceCopilotPage() {
  const { t } = useTranslation("inbox")
  const { conversation, customerDraftRef, replyDisabledReason } =
    useOutletContext<MobileCustomerConversationContext>()
  const { identity } = useMobileWorkspace()
  const conversationPath = `/inbox/customer/${conversation.id}`
  const back = useMobileBack(conversationPath)
  const [threadsOpen, setThreadsOpen] = useState(false)
  const copilot = useServiceCopilot({
    servedConversationID: conversation.id,
    currentUser: identity.user,
    customerDraftRef,
    active: true,
    onReplyApplied: back,
  })
  const loaded = copilot.threadsResource.data !== undefined

  return (
    <section className="absolute inset-0 flex min-h-0 flex-col bg-background">
      <MobilePageHeader
        backTo={conversationPath}
        title={
          loaded && copilot.threads.length > 0 ? (
            <CopilotThreadTitle copilot={copilot} onOpen={() => setThreadsOpen(true)} />
          ) : (
            t("contextAssistantTab")
          )
        }
        actions={
          loaded && copilot.threads.length > 0 ? (
            <Button
              variant="ghost"
              size="icon-lg"
              className="-mr-2"
              aria-label={t("copilotNewConversation")}
              title={t("copilotNewConversation")}
              disabled={copilot.drafting}
              onClick={copilot.startNewConversation}
            >
              <SquarePenIcon />
            </Button>
          ) : null
        }
      />
      {!loaded ? (
        <CopilotThreadsLoadState copilot={copilot} />
      ) : (
        <>
          {copilot.drafting ? <CopilotAgentSelect copilot={copilot} /> : null}
          <CopilotThreadView
            key={copilot.threadID}
            copilot={copilot}
            active
            applyReplyDisabledReason={replyDisabledReason}
          />
        </>
      )}
      <CopilotThreadSheet copilot={copilot} open={threadsOpen} onOpenChange={setThreadsOpen} />
      <CopilotApplyReplyDialog copilot={copilot} />
    </section>
  )
}

/** 标题单行展示当前线程的首条提问，点按打开线程列表。 */
function CopilotThreadTitle({
  copilot,
  onOpen,
}: {
  copilot: ServiceCopilot
  onOpen: () => void
}) {
  const { t } = useTranslation("inbox")
  return (
    <button
      type="button"
      className="mx-auto flex max-w-full min-w-0 items-center gap-1 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring"
      aria-label={t("copilotThreadPicker")}
      onClick={onOpen}
    >
      <span className="min-w-0 truncate">
        {copilot.selected?.title ?? t("copilotNewConversation")}
      </span>
      <ChevronDownIcon className="size-4 shrink-0 text-muted-foreground" />
    </button>
  )
}

/** 底部面板列出线程，选择后切换并关闭。 */
function CopilotThreadSheet({
  copilot,
  open,
  onOpenChange,
}: {
  copilot: ServiceCopilot
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation("inbox")
  const threadMeta = useCopilotThreadMeta()
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        showCloseButton={false}
        aria-describedby={undefined}
        className="max-h-[calc(100dvh-env(safe-area-inset-top)-1rem)] gap-0 rounded-t-2xl pb-[env(safe-area-inset-bottom)]"
        onOpenAutoFocus={focusDialogContainer}
      >
        <SheetHeader className="border-b">
          <SheetTitle>{t("copilotThreadPicker")}</SheetTitle>
        </SheetHeader>
        <div className="overflow-y-auto overscroll-contain py-1">
          {copilot.threads.map((thread) => {
            const current = thread.id === copilot.selected?.id
            return (
              <button
                key={thread.id}
                type="button"
                aria-current={current || undefined}
                className={cn(
                  "flex min-h-11 w-full items-center gap-3 px-4 py-2.5 text-left outline-none active:bg-muted focus-visible:bg-muted",
                  current && "bg-muted",
                )}
                onClick={() => {
                  copilot.openThread(thread.id)
                  onOpenChange(false)
                }}
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{thread.title}</span>
                  <span className="block truncate text-xs text-muted-foreground">{threadMeta(thread)}</span>
                </span>
                {current ? <CheckIcon className="size-4 shrink-0 text-primary" /> : null}
              </button>
            )
          })}
        </div>
      </SheetContent>
    </Sheet>
  )
}
