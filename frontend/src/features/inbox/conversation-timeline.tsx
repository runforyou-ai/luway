/** 展示各类会话的成员消息时间线、Agent 结果与发送状态。 */
import { type RefObject, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  ChatSubjectKind,
  ConversationSystemEventType,
  ConversationType,
  MessageType,
  type ConversationMessageListData,
  MessageVisibility,
  type CurrentUser,
  type ConversationMessageReference,
} from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useUserTimeZone } from "@/contexts/user-preferences"
import { useMemberChatPollingActive } from "@/features/inbox/use-member-chat-polling"
import type {
  OutgoingConversationDraft,
  OutgoingConversationMessage,
} from "@/lib/outgoing-message-store"

import { useConversationTimeline } from "./use-conversation-timeline"
import { conversationViewport, useConversationViewport } from "./use-conversation-viewport"
import { useConversationReading } from "./use-conversation-reading"
import { useConversationMessageNavigation } from "./use-conversation-message-navigation"
import { ConversationMentionNavigator } from "./conversation-mention-navigator"
import { AgentQueueState, AgentRunState } from "./agent-process"
import { createTimelineDateFormatters } from "./timeline-grouping"
import { TimelineMessageRow } from "./timeline-message-row"
import { mergeTimelineMessages, type TimelineMessage } from "./timeline-messages"
import { useTimelinePageSync } from "./use-timeline-page-sync"
import { useTimelineLocate } from "./use-timeline-locate"
import { useTimelineRowActions } from "./timeline-row-actions"

const pageLoaderCopy = {
  before: {
    className: "flex items-center justify-center gap-2 py-2",
    error: "messagesLoadEarlierError",
  },
  after: {
    className: "flex items-center justify-center gap-2 py-2",
    error: "messagesLoadLaterError",
  },
} as const

/** 时间线首尾的自动加载位置：接近视口时加载相邻消息，加载中显示状态，失败时显示原因与重试。 */
function TimelinePageLoader({
  direction,
  root,
  canLoad,
  loading,
  failed,
  disabled,
  onLoad,
}: {
  direction: "before" | "after"
  root: RefObject<HTMLDivElement | null>
  canLoad: boolean
  loading: boolean
  failed: boolean
  disabled: boolean
  onLoad: () => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const copy = pageLoaderCopy[direction]
  const sentinelRef = useRef<HTMLDivElement>(null)
  const onLoadRef = useRef(onLoad)
  useLayoutEffect(() => {
    onLoadRef.current = onLoad
  })
  useEffect(() => {
    const viewport = conversationViewport(root.current)
    const sentinel = sentinelRef.current
    if (!viewport || !sentinel || !canLoad || loading || failed || disabled) return
    // 距视口 200px 内即开始加载，失败后等待手动重试。
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) onLoadRef.current()
      },
      { root: viewport, rootMargin: "200px 0px" },
    )
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [root, canLoad, loading, failed, disabled])
  return (
    <div ref={sentinelRef} className={copy.className}>
      {loading ? <LoadingIndicator>{t("common:status.loading")}</LoadingIndicator> : null}
      {failed ? (
        <>
          <span className="text-xs text-destructive" role="status">
            {t(copy.error)}
          </span>
          <Button size="xs" variant="ghost" disabled={disabled} onClick={onLoad}>
            {t("common:actions.retry")}
          </Button>
        </>
      ) : null}
    </div>
  )
}

/** 返回最新窗口中第一条他人发送的未读消息编号；读到的位置早于窗口时取窗口内第一条他人消息，没有已读记录或未读时为空。 */
function firstUnreadMessageID(page: ConversationMessageListData, readThroughMessageID: string, identityID: string) {
  if (!readThroughMessageID) return ""
  const readIndex = page.messages.findIndex((message) => message.id === readThroughMessageID)
  if (readIndex < 0 && !page.hasEarlier) return ""
  const unread = page.messages.slice(readIndex + 1).find((message) =>
    message.type !== MessageType.MessageTypeSystem &&
    !(message.sender?.kind === ChatSubjectKind.ChatSubjectKindOrganizationIdentity && message.sender.sourceId === identityID),
  )
  return unread?.id ?? ""
}

/** 外部请求定位的消息，nonce 区分对同一消息的多次请求。 */
export type ConversationLocateTarget = { messageId: string; nonce: number }

/** 展示成员可见的会话历史和当前页面已发送消息。 */
function ConversationTimelineContent({
  conversationID,
  conversationType,
  requesterChatSubjectID = null,
  behalfAgentName = null,
  currentUser,
  requireWindowFocus = true,
  customerDeliveries = false,
  outgoingMessages,
  onRetryFailedMessage,
  onDiscardFailedMessage,
  retryFailedMessageDisabled = false,
  onReplyMessage,
  noteReplyEnabled = false,
  customerReplyUnavailable = false,
  replyVisibility = MessageVisibility.MessageVisibilityShared,
  onReadMessage,
  readThroughMessageID,
  prepareSendRef,
  mentionNavigation = true,
  onUnavailable,
  enabled = true,
  locateMessage = null,
  onApplyReply,
  applyReplyDisabledReason = null,
}: {
  conversationID: string
  conversationType: ConversationType
  /** 处理方查看服务会话时为发起人聊天主体编号，发起人发言显示在左侧。 */
  requesterChatSubjectID?: string | null
  /** 发起人查看自己与 AI 员工的服务聊天时为该 AI 员工名称，真人处理人的发言标注代其处理。 */
  behalfAgentName?: string | null
  currentUser: CurrentUser
  requireWindowFocus?: boolean
  customerDeliveries?: boolean
  outgoingMessages: OutgoingConversationMessage[]
  onRetryFailedMessage?: (message: OutgoingConversationDraft) => void
  onDiscardFailedMessage?: (clientMessageID: string) => void
  retryFailedMessageDisabled?: boolean
  onReplyMessage?: (
    message: ConversationMessageReference,
    visibility: MessageVisibility,
  ) => void
  noteReplyEnabled?: boolean
  customerReplyUnavailable?: boolean
  replyVisibility?: MessageVisibility
  onReadMessage?: (messageID: string) => void
  readThroughMessageID?: string | null
  prepareSendRef?: RefObject<(() => Promise<boolean>) | null>
  mentionNavigation?: boolean
  onUnavailable?: () => void
  enabled?: boolean
  locateMessage?: ConversationLocateTarget | null
  onApplyReply?: (body: string) => void
  applyReplyDisabledReason?: string | null
}) {
  const currentIdentityID = currentUser.identityId
  // 有文本发送中时，消息行禁用文本重试。
  const sendingText = outgoingMessages.some(
    (message) => message.status === "sending" && !message.attachment,
  )
  const { t, i18n } = useTranslation(["inbox", "common"])
  const timeZone = useUserTimeZone()
  const pollingActive = useMemberChatPollingActive({ requireWindowFocus })
  // 会话显示在可见窗口中即推进已读，不要求窗口获得焦点。
  const readingActive = useMemberChatPollingActive({ requireWindowFocus: false })
  const scrollRootRef = useRef<HTMLDivElement>(null)
  const viewportRef = useRef<{ keepPosition: () => void; followingLatest: () => boolean } | null>(null)
  const timeline = useConversationTimeline({
    conversationID,
    enabled,
    pollingActive,
    viewport: viewportRef,
  })
  const currentPage = timeline.page
  const { loading, error, refresh } = timeline
  useTimelinePageSync(conversationID, currentPage)
  const pageMessages = currentPage?.messages
  const visibleMessages = useMemo(
    () => mergeTimelineMessages(
      pageMessages ?? [],
      timeline.mode === "latest" ? outgoingMessages : [],
    ),
    [pageMessages, timeline.mode, outgoingMessages],
  )
  // 首个最新窗口到达时确定新消息起点，本次打开期间保持不变。
  const [unreadStart, setUnreadStart] = useState<string | null>(null)
  if (unreadStart === null && currentPage && timeline.mode === "latest" && !timeline.switching)
    setUnreadStart(firstUnreadMessageID(currentPage, readThroughMessageID ?? "", currentIdentityID))
  const unreadStartID = unreadStart ?? ""
  const viewport = useConversationViewport({
    unreadStartID,
    root: scrollRootRef,
    page: currentPage,
    mode: timeline.mode,
    switching: timeline.switching,
    visibleCount: visibleMessages.length,
    sentCount: outgoingMessages.length,
  })
  // 窗口重读按最新的视口入口判断是否贴底，并在合入前保存阅读位置。
  useLayoutEffect(() => {
    viewportRef.current = { keepPosition: viewport.keepReadingPosition, followingLatest: viewport.isFollowingLatest }
  })
  const location = useConversationMessageNavigation({
    root: scrollRootRef,
    page: currentPage,
    readingActive: pollingActive,
    openWindow: timeline.openWindow,
    cancelWindowUpdate: timeline.cancelWindowUpdate,
    viewport,
  })
  const reading = useConversationReading({
    root: scrollRootRef,
    page: currentPage,
    mode: timeline.mode,
    switching: timeline.switching || location.locating,
    readingActive: enabled && readingActive,
    identityID: currentIdentityID,
    atBottom: viewport.atBottom,
    getAtBottom: viewport.getAtBottom,
    onReadMessage,
    readThroughMessageID,
  })
  const service = requesterChatSubjectID !== null
  const summaryEventIDs = useMemo(
    () => (service ? serviceSummaryEventIDs(visibleMessages) : new Set<string>()),
    [service, visibleMessages],
  )

  const { mentions, hasLaterMessages, returnToLatest, followReference, loadPage } = useTimelineLocate({
    conversationID,
    conversationType,
    service,
    enabled,
    mentionNavigation,
    pollingActive,
    root: scrollRootRef,
    timeline,
    location,
    viewport,
    prepareSendRef,
    locateMessage,
    onUnavailable,
  })

  const { rowActions, renderCustomerReply } = useTimelineRowActions({
    onRetryFailedMessage,
    onDiscardFailedMessage,
    onReplyMessage,
    followReference,
    toggleProcess: viewport.stopFollowing,
    onApplyReply,
    applyReplyDisabledReason,
  })

  const dateFormatters = useMemo(
    () => createTimelineDateFormatters(i18n.resolvedLanguage, timeZone),
    [i18n.resolvedLanguage, timeZone],
  )

  if (loading && !currentPage && outgoingMessages.length === 0) {
    return (
      <LoadingIndicator className="min-h-0 flex-1 justify-center bg-background">
        {t("messagesLoading")}
      </LoadingIndicator>
    )
  }

  if (error && !currentPage && outgoingMessages.length === 0) {
    return <TimelineLoadFailure onRetry={() => void refresh()} />
  }

  const pageLoadDisabled = Boolean(timeline.loadingDirection) || timeline.switching
  return (
    <div className="relative min-h-0 flex-1 bg-background">
      <ScrollArea
        ref={scrollRootRef}
        // 将 Radix Viewport 的内联 table 布局覆盖为块级布局。
        className="h-full min-h-0 bg-background [&>[data-slot=scroll-area-viewport]>div]:!flex [&>[data-slot=scroll-area-viewport]>div]:!min-h-full [&>[data-slot=scroll-area-viewport]>div]:!flex-col"
      >
        <div className="flex w-full flex-1 flex-col px-3.5 pb-2.5 md:px-5">
          {currentPage?.hasEarlier || timeline.pageError === "before" ? (
            <TimelinePageLoader
              direction="before"
              root={scrollRootRef}
              canLoad={Boolean(currentPage?.hasEarlier)}
              loading={timeline.loadingDirection === "before"}
              failed={timeline.pageError === "before"}
              disabled={pageLoadDisabled}
              onLoad={() => void loadPage("before")}
            />
          ) : null}

          {visibleMessages.length === 0 ? (
            <div className="p-6 text-center text-sm text-muted-foreground">
              {t("messagesEmpty")}
            </div>
          ) : null}
          <div className="flex flex-col">
            {visibleMessages.map((message, index) => (
              <TimelineMessageRow
                key={message.id}
                message={message}
                previous={visibleMessages[index - 1]}
                next={visibleMessages[index + 1]}
                conversationID={conversationID}
                conversationType={conversationType}
                requesterChatSubjectID={requesterChatSubjectID}
                behalfAgentName={behalfAgentName}
                currentUser={currentUser}
                formatters={dateFormatters}
                highlighted={location.highlightedID === message.id}
                summaryEvent={summaryEventIDs.has(message.id)}
                unreadStart={message.id === unreadStartID}
                customerDeliveries={customerDeliveries}
                // 发送中状态只传给失败消息，其余行的 memo 保持有效。
                sendingText={message.deliveryStatus === "failed" && sendingText}
                retryFailedMessageDisabled={retryFailedMessageDisabled}
                onRetryFailedMessage={onRetryFailedMessage ? rowActions.retryFailedMessage : undefined}
                onDiscardFailedMessage={onDiscardFailedMessage ? rowActions.discardFailedMessage : undefined}
                onReplyMessage={onReplyMessage ? rowActions.replyMessage : undefined}
                noteReplyEnabled={noteReplyEnabled}
                customerReplyUnavailable={customerReplyUnavailable}
                replyVisibility={replyVisibility}
                onFollowReference={rowActions.followReference}
                onToggleProcess={rowActions.toggleProcess}
                renderCodeBlock={onApplyReply ? renderCustomerReply : undefined}
              />
            ))}
          </div>
          {timeline.mode === "latest" && !currentPage?.hasLater ? (
            <TimelineAgentFooter
              page={currentPage}
              conversationID={conversationID}
              conversationType={conversationType}
              service={service}
              onStopped={timeline.refresh}
              onToggle={viewport.stopFollowing}
            />
          ) : null}
          {currentPage?.hasLater && timeline.mode === "anchor" ? (
            <TimelinePageLoader
              direction="after"
              root={scrollRootRef}
              canLoad
              loading={timeline.loadingDirection === "after"}
              failed={timeline.pageError === "after"}
              disabled={pageLoadDisabled}
              onLoad={() => void loadPage("after")}
            />
          ) : null}
        </div>
      </ScrollArea>
      {timeline.refreshError || (error && !currentPage) ? (
        <button
          type="button"
          className="absolute top-2 left-1/2 z-10 min-h-8 -translate-x-1/2 rounded-full border bg-background/95 px-3 text-xs text-warning shadow-sm backdrop-blur"
          disabled={loading}
          onClick={() => void refresh()}
        >
          {currentPage
            ? t("messagesRefreshError")
            : `${t("messagesLoadError")} · ${t("common:actions.retry")}`}
        </button>
      ) : null}
      <ConversationMentionNavigator
        navigation={mentions}
        showLatest={!viewport.atBottom || hasLaterMessages}
        newCount={timeline.mode === "latest" ? reading.newCount : 0}
        busy={timeline.switching}
        onLatest={() => void returnToLatest()}
      />
    </div>
  )
}

/** 首次读取消息失败时占满时间线的提示与重试。 */
function TimelineLoadFailure({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation(["inbox", "common"])
  return (
    <div className="flex min-h-0 flex-1 items-center justify-center bg-background p-6 text-center">
      <div>
        <p className="text-sm text-muted-foreground">
          {t("messagesLoadError")}
        </p>
        <Button
          className="mt-4"
          size="sm"
          variant="outline"
          onClick={onRetry}
        >
          {t("common:actions.retry")}
        </Button>
      </div>
    </div>
  )
}

/** 返回服务会话中承载周期小结的系统事件：每个周期最后一次关闭事件，重新打开的周期不计入。 */
function serviceSummaryEventIDs(messages: readonly TimelineMessage[]) {
  const latestClosed = new Map<string, string>()
  for (const message of messages) {
    const event = message.systemEvent
    if (event?.serviceSessionId && event.type === ConversationSystemEventType.ConversationSystemEventServiceSessionClosed) {
      latestClosed.set(event.serviceSessionId, message.id)
    }
    if (event?.serviceSessionId && event.type === ConversationSystemEventType.ConversationSystemEventServiceSessionReopened) {
      latestClosed.delete(event.serviceSessionId)
    }
  }
  return new Set(latestClosed.values())
}

/** 最新窗口末尾的运行中 AI 回复与排队中的 AI 员工；服务会话以外的对话提供停止运行入口。 */
function TimelineAgentFooter({
  page,
  conversationID,
  conversationType,
  service,
  onStopped,
  onToggle,
}: {
  page: ConversationMessageListData | null
  conversationID: string
  conversationType: ConversationType
  service: boolean
  onStopped: () => Promise<unknown>
  onToggle: () => void
}) {
  const stoppable =
    !service &&
    (conversationType === ConversationType.ConversationTypeAgent ||
      conversationType === ConversationType.ConversationTypeGroup ||
      conversationType === ConversationType.ConversationTypeCopilot)
  const group = conversationType === ConversationType.ConversationTypeGroup
  const copilot = conversationType === ConversationType.ConversationTypeCopilot
  return (
    <>
      {(page?.agentRuns ?? []).map((run) => (
        <AgentRunState
          key={run.id}
          run={run}
          onStopped={onStopped}
          conversationID={stoppable ? conversationID : undefined}
          group={group}
          copilot={copilot}
          incoming={!service}
          onToggle={onToggle}
        />
      ))}
      <AgentQueueState agents={page?.pendingAgents ?? []} copilot={copilot} incoming={!service} />
    </>
  )
}

/** 切换会话时重新建立独立的窗口、定位和阅读状态。 */
export function ConversationTimeline(
  props: Parameters<typeof ConversationTimelineContent>[0],
) {
  return <ConversationTimelineContent key={props.conversationID} {...props} />
}
