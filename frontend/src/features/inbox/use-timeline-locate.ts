/** 时间线的失权恢复、提及导航、回到最新、引用与检索定位及相邻页加载。 */
import { type RefObject, useCallback, useEffect, useEffectEvent, useRef } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { ConversationType, isApiError, isNotFoundApiError } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { recoverSession } from "@/lib/session-navigation"

import type { ConversationLocateTarget } from "./conversation-timeline"
import { useConversationMentionNavigation } from "./use-conversation-mention-navigation"
import type { useConversationMessageNavigation } from "./use-conversation-message-navigation"
import type { useConversationTimeline } from "./use-conversation-timeline"
import type { useConversationViewport } from "./use-conversation-viewport"

/** 组合时间线窗口、视口与消息定位，提供提及导航、回到最新、引用跳转和相邻页加载；会话不可访问时调用 onUnavailable，未提供时重读会话摘要。 */
export function useTimelineLocate({
  conversationID,
  conversationType,
  service,
  enabled,
  mentionNavigation,
  pollingActive,
  root,
  timeline,
  location,
  viewport,
  prepareSendRef,
  locateMessage,
  onUnavailable,
}: {
  conversationID: string
  conversationType: ConversationType
  service: boolean
  enabled: boolean
  mentionNavigation: boolean
  pollingActive: boolean
  root: RefObject<HTMLDivElement | null>
  timeline: ReturnType<typeof useConversationTimeline>
  location: ReturnType<typeof useConversationMessageNavigation>
  viewport: ReturnType<typeof useConversationViewport>
  prepareSendRef?: RefObject<(() => Promise<boolean>) | null>
  locateMessage: ConversationLocateTarget | null
  onUnavailable?: () => void
}) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const currentPage = timeline.page

  /** 当前成员失去会话访问权时恢复到会话列表。 */
  const handleUnavailable = useCallback(() => {
    if (onUnavailable) {
      onUnavailable()
      return
    }
    void invalidate(resourceKeys.conversationSummary(conversationID))
  }, [conversationID, invalidate, onUnavailable])

  useEffect(() => {
    if (isNotFoundApiError(timeline.error) || isNotFoundApiError(timeline.refreshError)) handleUnavailable()
  }, [timeline.error, timeline.refreshError, handleUnavailable])

  /** 处理读取窗口失败：会话不可访问时恢复，会话失效时进入会话入口，其余提示给定文案。 */
  const reportLoadError = useCallback((error: unknown, message: string) => {
    if (isApiError(error) && error.reason === "conversation_unavailable") handleUnavailable()
    else if (!recoverSession(error, navigate)) toast.error(message)
  }, [handleUnavailable, navigate])

  const mentions = useConversationMentionNavigation({
    conversationID,
    enabled:
      enabled &&
      mentionNavigation &&
      (conversationType === ConversationType.ConversationTypeGroup || service),
    pollingActive,
    root,
    page: currentPage,
    switching: timeline.switching || location.locating,
    locate: location.locate,
    cancel: location.cancel,
    onUnavailable: handleUnavailable,
  })

  // 按分页边界判断后续消息，群聊同时比较导航态的最新序号。
  const windowLastSequence =
    currentPage?.messages[currentPage.messages.length - 1]?.messageSeq
  const hasLaterMessages = Boolean(
    currentPage?.hasLater ||
    (windowLastSequence &&
      BigInt(mentions.latestSequence) > BigInt(windowLastSequence)),
  )

  /** 读取最新窗口成功后结束本轮并恢复贴底。 */
  const returnToLatest = useCallback(async () => {
    mentions.pause()
    try {
      if (!(await timeline.openWindow())) return false
      mentions.close()
      viewport.followLatest()
      return true
    } catch (error) {
      reportLoadError(error, error instanceof Error ? error.message : t("messagesLoadError"))
      return false
    }
  }, [
    mentions.pause,
    mentions.close,
    timeline.openWindow,
    viewport.followLatest,
    reportLoadError,
    t,
  ])

  useEffect(() => {
    if (!prepareSendRef) return
    prepareSendRef.current = () =>
      timeline.mode === "latest" && !timeline.switching
        ? Promise.resolve(true)
        : returnToLatest()
    return () => {
      prepareSendRef.current = null
    }
  }, [prepareSendRef, returnToLatest, timeline.mode, timeline.switching])

  /** 引用跳转暂停提及确认，失败保留原窗口。 */
  async function followReference(messageID: string) {
    mentions.pause()
    try {
      await location.locate(messageID)
    } catch (error) {
      if (isApiError(error) && error.reason === "message_unavailable") {
        // 重读当前窗口，引用状态以服务端结果为准。
        void timeline.refresh()
        toast.message(t("messageOriginalDeleted"))
      } else {
        reportLoadError(error, error instanceof Error ? error.message : t("messagesLoadError"))
      }
    }
  }

  // 检索结果请求定位时，等首屏窗口就绪后复用引用跳转流程，同一请求只处理一次。
  const locatedNonceRef = useRef(0)
  const locateRequested = useEffectEvent((messageID: string) => {
    void followReference(messageID)
  })
  useEffect(() => {
    if (!locateMessage || !currentPage || locatedNonceRef.current === locateMessage.nonce) return
    locatedNonceRef.current = locateMessage.nonce
    locateRequested(locateMessage.messageId)
  }, [locateMessage, currentPage])

  /** 加载相邻历史页并保持可见消息位置，失败原因显示在加载位置。 */
  async function loadPage(direction: "before" | "after") {
    try {
      await timeline.loadPage(direction, viewport.preservePosition)
    } catch (error) {
      if (isApiError(error) && error.reason === "conversation_unavailable") handleUnavailable()
      else recoverSession(error, navigate)
    }
  }

  return { mentions, hasLaterMessages, returnToLatest, followReference, loadPage }
}
