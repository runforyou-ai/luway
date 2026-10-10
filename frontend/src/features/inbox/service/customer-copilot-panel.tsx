/** 客户会话的 AI 助手：切换和新建 Copilot 线程，在线程中提问并把 AI 回复填入对客草稿；桌面侧栏与移动端子页共用线程状态和对话视图。 */
import type { ComposerDraftBridge } from "@/features/inbox/composer/conversation-composer-types"
import { useEffect, useRef, useState, type RefObject } from "react"
import { ChevronDownIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  ConversationType,
  listServiceCopilotThreads,
  listServiceReplyAgents,
  sendFirstServiceCopilotMessage,
  type ConversationMessage,
  type CurrentUser,
  type ServiceCopilotThread,
  type DirectTextMessageInput,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { NativeSelect } from "@/components/ui/native-select"
import { useWorkspace } from "@/contexts/workspace-context"
import {
  ConversationComposer,
} from "@/features/inbox/composer/conversation-composer"
import { ConversationTimeline } from "@/features/inbox/timeline/conversation-timeline"
import { selectServiceReplyAgentID } from "@/features/inbox/composer/customer-reply-agent"
import { useConversationTime, useMinuteTick } from "@/features/inbox/shared/use-conversation-time"
import { useThreadComposerBridge } from "@/features/inbox/composer/use-thread-composer-bridge"
import { resourceKeys } from "@/hooks/resource-keys"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { readLocalPreference, writeLocalPreference } from "@/lib/local-preference"
import { cn } from "@/lib/utils"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 本页内各客户会话最近查看的线程，页面刷新后回到最近更新的线程。 */
const lastViewedThreads = new Map<string, string>()

/** 客户会话 AI 助手的线程、AI 员工选择与填入回复状态。 */
export type ServiceCopilot = ReturnType<typeof useServiceCopilot>

/** 读取客户会话的 Copilot 线程，维护当前线程、新对话草稿和填入对客草稿；onReplyApplied 在回复写入草稿后调用。 */
export function useServiceCopilot({
  servedConversationID,
  currentUser,
  customerDraftRef,
  active,
  onReplyApplied,
}: {
  servedConversationID: string
  currentUser: CurrentUser
  customerDraftRef: RefObject<ComposerDraftBridge | null>
  active: boolean
  onReplyApplied?: () => void
}) {
  const { t } = useTranslation("inbox")
  const invalidate = useResourceInvalidator()
  const aliveRef = useMountedRef()
  const threadsKey = resourceKeys.serviceCopilotThreads(servedConversationID)
  const threadsResource = useResource(
    threadsKey,
    () => listServiceCopilotThreads(servedConversationID),
    // 线程变更通知失效线程列表。
    { staleTime: 0 },
  )
  const threads = threadsResource.data ?? []
  const [selectedID, setSelectedID] = useState(() => lastViewedThreads.get(servedConversationID) ?? "")
  const [composingNew, setComposingNew] = useState(false)
  const [draftID, setDraftID] = useState(() => window.crypto.randomUUID())
  const [pendingReply, setPendingReply] = useState<string | null>(null)
  const storageKey = `app.inbox.copilot.${currentUser.identityId}`
  const [preferredAgentID, setPreferredAgentID] = useState(() => {
    // 读取本人上次新建对话选择的 AI 员工，未保存时不预选。
    const stored = readLocalPreference(storageKey)
    return typeof stored === "string" ? stored : ""
  })

  // 优先展示本页上次查看且仍存在的线程，否则展示最近更新的线程；没有线程时进入新对话。
  const selected = composingNew
    ? null
    : (threads.find((thread) => thread.id === selectedID) ?? threads[0] ?? null)

  useEffect(() => {
    // 首次进入或所选线程消失时固定到实际展示的线程，列表顺序变化时保持当前视图。
    if (!selected || selected.id === selectedID) return
    lastViewedThreads.set(servedConversationID, selected.id)
    setSelectedID(selected.id)
  }, [servedConversationID, selected, selectedID])
  const drafting = threadsResource.data !== undefined && !selected
  const agentOptions = useResource(
    resourceKeys.serviceReplyAgents(),
    listServiceReplyAgents,
    { enabled: drafting && active, staleTime: 0 },
  )
  const agents = agentOptions.data ?? []
  const agentIdentityID = selectServiceReplyAgentID(agents, preferredAgentID)
  const threadID = selected?.id ?? draftID

  /** 切换到指定线程并记住本页的查看位置。 */
  function openThread(id: string) {
    lastViewedThreads.set(servedConversationID, id)
    setSelectedID(id)
    setComposingNew(false)
  }

  /** 新对话首条提问或附件保存后，刷新线程列表并切换到该线程。 */
  async function openCreatedThread(id: string) {
    lastViewedThreads.set(servedConversationID, id)
    await invalidate(threadsKey)
    if (aliveRef.current) openThread(id)
  }

  /** 把 AI 回复写入对客草稿并通知调用方。 */
  function replaceDraft(body: string) {
    customerDraftRef.current?.replace(body)
    onReplyApplied?.()
  }

  return {
    servedConversationID,
    currentUser,
    threadsResource,
    threads,
    selected,
    drafting,
    threadID,
    agents,
    agentIdentityID,
    draftDisabledReason: agentOptions.error
      ? t("replyAssistantAgentsLoadError")
      : agentOptions.data !== undefined && agents.length === 0
        ? t("agentPickerEmpty")
        : null,
    pendingReply,
    openThread,
    openCreatedThread,
    /** 以新的草稿编号进入新对话。 */
    startNewConversation() {
      setDraftID(window.crypto.randomUUID())
      setComposingNew(true)
    },
    /** 记住本人新建对话选择的 AI 员工。 */
    selectAgent(value: string) {
      setPreferredAgentID(value)
      writeLocalPreference(storageKey, value)
    },
    /** 按草稿是否为空直接填入或等待确认后替换对客回复草稿。 */
    applyReply(body: string) {
      const bridge = customerDraftRef.current
      if (!bridge) return
      if (bridge.read().trim()) {
        setPendingReply(body)
        return
      }
      replaceDraft(body)
    },
    /** 确认替换已有草稿。 */
    confirmPendingReply() {
      if (pendingReply === null) return
      setPendingReply(null)
      replaceDraft(pendingReply)
    },
    /** 放弃待确认的替换。 */
    cancelPendingReply() {
      setPendingReply(null)
    },
  }
}

/** 返回线程的 AI 员工、创建人和最近更新时间摘要。 */
export function useCopilotThreadMeta() {
  const { t } = useTranslation("inbox")
  const formatTime = useConversationTime()
  useMinuteTick()
  return (thread: ServiceCopilotThread) =>
    t("copilotThreadMeta", {
      agent: thread.agentName,
      creator: thread.createdByName,
      time: formatTime(thread.lastActivityAt),
    })
}

/** 线程列表尚未读取完成时展示加载或重试。 */
export function CopilotThreadsLoadState({ copilot }: { copilot: ServiceCopilot }) {
  const { t } = useTranslation(["inbox", "common"])
  const mobile = resolveAppPlatform() === "mobile"
  return copilot.threadsResource.error ? (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
      <p className="text-sm text-muted-foreground">{t("copilotThreadsLoadError")}</p>
      <Button
        size={mobile ? "default" : "sm"}
        variant="outline"
        className={cn(mobile && "min-h-11")}
        onClick={() => void copilot.threadsResource.refresh()}
      >
        {t("common:actions.retry")}
      </Button>
    </div>
  ) : (
    <LoadingIndicator className="flex-1 justify-center">{t("common:status.loading")}</LoadingIndicator>
  )
}

/** 新对话的 AI 员工选择行。 */
export function CopilotAgentSelect({ copilot }: { copilot: ServiceCopilot }) {
  const { t } = useTranslation("inbox")
  const mobile = resolveAppPlatform() === "mobile"
  const id = `copilot-agent-${copilot.servedConversationID}`
  return (
    <div className={cn("flex shrink-0 items-center border-b", mobile ? "gap-3 px-4 py-2" : "gap-2 px-3 py-2")}>
      <label htmlFor={id} className={cn("shrink-0 text-muted-foreground", mobile ? "text-sm" : "text-xs")}>
        {t("copilotAgent")}
      </label>
      <NativeSelect
        id={id}
        className={cn("min-w-0 flex-1", mobile ? "min-h-11 text-sm" : "h-8 text-xs shadow-none")}
        value={copilot.agentIdentityID}
        disabled={copilot.agents.length === 0}
        onChange={(event) => copilot.selectAgent(event.target.value)}
      >
        {copilot.agents.map((agent) => (
          <option key={agent.identityId} value={agent.identityId}>
            {agent.displayName}
          </option>
        ))}
      </NativeSelect>
    </div>
  )
}

/** 替换已有对客草稿前的确认弹窗。 */
export function CopilotApplyReplyDialog({ copilot }: { copilot: ServiceCopilot }) {
  const { t } = useTranslation("inbox")
  const appliedRef = useRef(false)
  return (
    <ConfirmationDialog
      open={copilot.pendingReply !== null}
      pending={false}
      destructive={false}
      title={t("copilotApplyReplyConfirmTitle")}
      description={t("copilotApplyReplyConfirmDescription")}
      onOpenChange={(open) => !open && copilot.cancelPendingReply()}
      onConfirm={() => {
        appliedRef.current = true
        copilot.confirmPendingReply()
      }}
      onCloseAutoFocus={(event) => {
        // 替换草稿后焦点交给回复输入框。
        if (!appliedRef.current) return
        appliedRef.current = false
        event.preventDefault()
      }}
    />
  )
}

/** 展示客户会话右侧栏的 Copilot 线程，没有线程或新建对话时直接展示 AI 员工选择与提问输入框。 */
export function ServiceCopilotPanel({
  servedConversationID,
  replyDisabledReason,
  customerDraftRef,
  active,
}: {
  servedConversationID: string
  replyDisabledReason: string | null
  customerDraftRef: RefObject<ComposerDraftBridge | null>
  active: boolean
}) {
  const { t } = useTranslation("inbox")
  const { identity } = useWorkspace()
  const copilot = useServiceCopilot({
    servedConversationID,
    currentUser: identity.user,
    customerDraftRef,
    active,
  })

  if (copilot.threadsResource.data === undefined) return <CopilotThreadsLoadState copilot={copilot} />

  return (
    <>
      {copilot.threads.length > 0 ? (
        <div className="flex shrink-0 items-center gap-2 px-3 py-2">
          <CopilotThreadPicker copilot={copilot} />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="shrink-0"
            disabled={copilot.drafting}
            onClick={copilot.startNewConversation}
          >
            {t("copilotNewConversation")}
          </Button>
        </div>
      ) : null}
      {copilot.drafting ? <CopilotAgentSelect copilot={copilot} /> : null}
      <CopilotThreadView
        key={copilot.threadID}
        copilot={copilot}
        active={active}
        applyReplyDisabledReason={replyDisabledReason}
      />
      <CopilotApplyReplyDialog copilot={copilot} />
    </>
  )
}

/** 以首条提问为主文字列出线程，并显示 AI 员工、创建人和最近更新时间。 */
function CopilotThreadPicker({ copilot }: { copilot: ServiceCopilot }) {
  const { t } = useTranslation("inbox")
  const threadMeta = useCopilotThreadMeta()
  const { selected } = copilot

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-auto min-h-8 min-w-0 flex-1 justify-between gap-2 px-2 py-1 text-left"
          aria-label={t("copilotThreadPicker")}
        >
          <span className="min-w-0">
            <span className={cn("block truncate text-sm", selected ? "font-medium" : "font-normal text-muted-foreground")}>
              {selected?.title ?? t("copilotThreadPicker")}
            </span>
            {selected ? (
              <span className="block truncate text-xs font-normal text-muted-foreground">
                {threadMeta(selected)}
              </span>
            ) : null}
          </span>
          <ChevronDownIcon className="shrink-0 text-muted-foreground" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-[var(--radix-dropdown-menu-trigger-width)] min-w-64 overflow-y-auto">
        {copilot.threads.map((thread) => (
          <DropdownMenuItem
            key={thread.id}
            className="flex-col items-start gap-0.5"
            onSelect={() => copilot.openThread(thread.id)}
          >
            <span className="w-full truncate text-sm">{thread.title}</span>
            <span className="w-full truncate text-xs text-muted-foreground">{threadMeta(thread)}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** 展示当前线程或新对话的时间线与提问输入框；调用方按 copilot.threadID 设置 key。 */
export function CopilotThreadView({
  copilot,
  active,
  applyReplyDisabledReason,
}: {
  copilot: ServiceCopilot
  active: boolean
  applyReplyDisabledReason: string | null
}) {
  const { t } = useTranslation("inbox")
  const mobile = resolveAppPlatform() === "mobile"
  const { selected: thread, threadID, currentUser } = copilot
  const bridge = useThreadComposerBridge(threadID)
  const disabledReason = thread
    ? (thread.agentActive ? null : t("copilotAgentUnavailable"))
    : copilot.draftDisabledReason
  const sendFirstMessage = thread
    ? undefined
    : async (input: DirectTextMessageInput): Promise<ConversationMessage> => {
        const result = await sendFirstServiceCopilotMessage(copilot.servedConversationID, {
          threadId: threadID,
          agentIdentityId: copilot.agentIdentityID,
          clientMessageId: input.clientMessageId,
          body: input.body,
        })
        await copilot.openCreatedThread(result.thread.id)
        return result.message
      }

  return (
    <>
      <ConversationTimeline
        {...bridge.timeline}
        conversationID={threadID}
        conversationType={ConversationType.Copilot}
        currentUser={currentUser}
        requireWindowFocus={!mobile}
        retryFailedMessageDisabled={Boolean(disabledReason)}
        onReplyMessage={thread && !disabledReason ? bridge.selectReplyTarget : undefined}
        mentionNavigation={false}
        enabled={Boolean(thread) && active}
        onApplyReply={copilot.applyReply}
        applyReplyDisabledReason={applyReplyDisabledReason}
      />
      <ConversationComposer
        {...bridge.composer}
        conversationID={thread ? threadID : ""}
        conversationType={ConversationType.Copilot}
        disabledReason={disabledReason}
        submitOnEnter={!mobile}
        refocusAfterSubmit={!mobile}
        currentIdentityID={currentUser.identityId}
        onSucceeded={() => void copilot.threadsResource.refresh()}
        sendIndividualMessage={sendFirstMessage}
        attachmentAgentDraft={
          thread
            ? undefined
            : {
                conversationID: threadID,
                agentIdentityID: copilot.agentIdentityID,
                servedConversationID: copilot.servedConversationID,
              }
        }
        onAttachmentConversationCreated={(_, conversationID) => void copilot.openCreatedThread(conversationID)}
      />
    </>
  )
}
