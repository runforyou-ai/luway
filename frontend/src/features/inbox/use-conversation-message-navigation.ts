/** 统一消息定位、可见确认及整行高亮，隔离过期跳转。 */
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type RefObject,
} from "react"
import type { ConversationMessageListData } from "@/api"
import { conversationViewport, type useConversationViewport } from "./use-conversation-viewport"
import { isConversationMessageVisible } from "./conversation-message-visibility"

type PendingLocation = {
  id: string
  revision: number
  resolve: (visible: boolean) => void
}

/** 定位消息并在当前窗口实际可见时完成确认。 */
export function useConversationMessageNavigation({
  root,
  page,
  readingActive,
  openWindow,
  cancelWindowUpdate,
  viewport,
}: {
  root: RefObject<HTMLDivElement | null>
  page: ConversationMessageListData | null
  readingActive: boolean
  openWindow: (messageID?: string) => Promise<boolean>
  cancelWindowUpdate: () => void
  viewport: ReturnType<typeof useConversationViewport>
}) {
  const { holdForLocation, releaseLocation, revealMessage } = viewport
  const revision = useRef(0)
  const pending = useRef<PendingLocation | null>(null)
  const positioned = useRef(0)
  const highlightTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [target, setTarget] = useState<{ id: string; revision: number } | null>(
    null,
  )
  const [highlightedID, setHighlightedID] = useState<string | null>(null)
  const [locating, setLocating] = useState(false)

  /** 结束未完成的定位并清理旧高亮。 */
  const cancel = useCallback(() => {
    revision.current += 1
    releaseLocation()
    pending.current?.resolve(false)
    pending.current = null
    if (highlightTimer.current !== null) clearTimeout(highlightTimer.current)
    cancelWindowUpdate()
    setTarget(null)
    setHighlightedID(null)
    setLocating(false)
  }, [cancelWindowUpdate, releaseLocation])

  /** 用统一窗口入口定位目标，等待实际视口确认。 */
  const locate = useCallback(
    async (id: string) => {
      cancel()
      const current = revision.current
      holdForLocation()
      setLocating(true)
      try {
        if (!(await openWindow(id)) || revision.current !== current)
          return false
        return await new Promise<boolean>((resolve) => {
          pending.current = { id, revision: current, resolve }
          setTarget({ id, revision: current })
        })
      } finally {
        if (revision.current === current) {
          releaseLocation()
          setLocating(false)
        }
      }
    },
    [cancel, openWindow, holdForLocation, releaseLocation],
  )

  useLayoutEffect(() => {
    if (!target || !readingActive || positioned.current === target.revision)
      return
    if (!revealMessage(target.id)) return
    positioned.current = target.revision
    setHighlightedID(target.id)
    highlightTimer.current = setTimeout(() => setHighlightedID(null), 2000)
  }, [target, page, readingActive, revealMessage])

  useEffect(() => {
    if (!target || !readingActive) return
    const viewport = conversationViewport(root.current)
    const row = viewport?.querySelector<HTMLElement>(
      `[data-message-id="${target.id}"]`,
    )
    if (!viewport || !row) return
    /** 只确认仍属于当前跳转且正文可见的目标。 */
    function checkVisible() {
      if (!viewport || !row || pending.current?.revision !== target?.revision)
        return
      const bounds = viewport.getBoundingClientRect()
      const rect = row.getBoundingClientRect()
      if (!isConversationMessageVisible(rect, bounds)) return
      pending.current?.resolve(true)
      pending.current = null
    }
    const observer = new IntersectionObserver(checkVisible, {
      root: viewport,
      threshold: [0, 0.25, 0.5, 1],
    })
    observer.observe(row)
    checkVisible()
    return () => observer.disconnect()
  }, [target, page, readingActive, root])

  useEffect(
    () => () => {
      revision.current += 1
      pending.current?.resolve(false)
      if (highlightTimer.current !== null) clearTimeout(highlightTimer.current)
    },
    [],
  )
  return { locate, cancel, locating, highlightedID }
}
