/** 正常浏览时确认视口内的提及，不移动滚动位置或普通已读水位。 */
import { useEffect, useEffectEvent, useRef, useState, type RefObject } from "react"
import { ConversationMentionReviewOutcome, markConversationMentionReviewed, type ConversationMessageListData } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { conversationViewport } from "./use-conversation-viewport"
import { isConversationMessageVisible } from "./conversation-message-visibility"

/** 观察当前待查看目标，只确认激活页面内实际可见的消息。 */
export function useVisibleConversationMentions({
  conversationID, root, page, pendingIDs, pendingUpdatedAt, enabled, onReviewed,
}: {
  conversationID: string
  root: RefObject<HTMLDivElement | null>
  page: ConversationMessageListData | null
  pendingIDs: string[] | undefined
  pendingUpdatedAt: number
  enabled: boolean
  onReviewed: (id: string) => void
}) {
  const invalidate = useResourceInvalidator()
  const submitted = useRef(new Set<string>())
  const conversationLifetime = useRef({ active: false })
  const [visibleIDs, setVisibleIDs] = useState<Set<string>>(new Set())
  const notifyReviewed = useEffectEvent(onReviewed)

  useEffect(() => {
    const lifetime = { active: true }
    conversationLifetime.current = lifetime
    submitted.current = new Set()
    return () => { lifetime.active = false }
  }, [conversationID])

  // 每次列表刷新重新检查可见目标，失败确认在待查看目标下次刷新时重试。
  useEffect(() => {
    const viewport = conversationViewport(root.current)
    if (!enabled || !viewport || !page || !pendingIDs) {
      setVisibleIDs(new Set())
      return
    }
    let current = true
    const lifetime = conversationLifetime.current
    const sent = submitted.current
    const pending = new Set(pendingIDs)
    const nodes = [...viewport.querySelectorAll<HTMLElement>("[data-message-id]")]
      .filter((node) => pending.has(node.dataset.messageId!))

    /** 同步可见目标并提交尚未发送的单条查看确认。 */
    function checkVisible() {
      if (!viewport || !current) return
      const bounds = viewport.getBoundingClientRect()
      const visible = new Set<string>()
      for (const node of nodes) {
        if (!isConversationMessageVisible(node.getBoundingClientRect(), bounds)) continue
        const id = node.dataset.messageId!
        visible.add(id)
        if (sent.has(id)) continue
        sent.add(id)
        void markConversationMentionReviewed(conversationID, id)
          .then((result) => {
            // 视口或查询刷新不作废已经发生的查看，只在离开会话后忽略界面结果。
            if (lifetime.active && result.outcome !== ConversationMentionReviewOutcome.ConversationMentionUnavailable) notifyReviewed(id)
            return Promise.all([
              invalidate(resourceKeys.conversationNavigation(conversationID)),
              invalidate(resourceKeys.conversationMentions(conversationID)),
            ])
          })
          .catch((error: unknown) => {
            sent.delete(id)
            if (!lifetime.active) return
            console.warn("确认可见提及失败", { conversationId: conversationID, messageId: id, error })
            setVisibleIDs((previous) => {
              const next = new Set(previous)
              next.delete(id)
              return next
            })
          })
      }
      setVisibleIDs((previous) => previous.size === visible.size &&
        [...visible].every((id) => previous.has(id)) ? previous : visible)
    }

    const observer = new ResizeObserver(checkVisible)
    observer.observe(viewport)
    if (viewport.firstElementChild) observer.observe(viewport.firstElementChild)
    viewport.addEventListener("scroll", checkVisible, { passive: true })
    checkVisible()
    return () => {
      current = false
      observer.disconnect()
      viewport.removeEventListener("scroll", checkVisible)
    }
  }, [conversationID, root, page, pendingIDs, pendingUpdatedAt, enabled, invalidate])

  return visibleIDs
}
