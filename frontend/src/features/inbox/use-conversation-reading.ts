/** 按实际可见范围推进普通已读，并统计当前窗口尾端之后的新消息。 */
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type RefObject,
} from "react"
import { ChatSubjectKind, MessageType, type ConversationMessageListData } from "@/api"
import { compareConversationMessages } from "./conversation-window"
import { MessageReadObserver } from "./message-read-observer"
import { conversationViewport } from "./use-conversation-viewport"

/** 读取统一视口状态并维护连续可见已读。 */
export function useConversationReading({
  root,
  page,
  mode,
  switching,
  readingActive,
  identityID,
  atBottom,
  getAtBottom,
  onReadMessage,
  readThroughMessageID,
}: {
  root: RefObject<HTMLDivElement | null>
  page: ConversationMessageListData | null
  mode: "latest" | "anchor"
  switching: boolean
  readingActive: boolean
  identityID: string
  atBottom: boolean
  getAtBottom: () => boolean
  onReadMessage?: (id: string) => void
  readThroughMessageID?: string | null
}) {
  const readID = useRef(readThroughMessageID ?? "")
  const queuedID = useRef("")
  const seen = useRef(new Set<string>())
  const previousLast = useRef<ConversationMessageListData["messages"][number] | null>(null)
  const newMessages = useRef(new Set<string>())
  const readTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [newCount, setNewCount] = useState(0)

  /** 合并同一窗口连续产生的已读写入。 */
  const queueRead = useCallback(
    (id: string, immediate = false) => {
      if (!onReadMessage || id === readID.current || id === queuedID.current)
        return
      queuedID.current = id
      if (readTimer.current) clearTimeout(readTimer.current)
      if (immediate) {
        readID.current = id
        queuedID.current = ""
        onReadMessage(id)
      } else
        readTimer.current = setTimeout(() => {
          readID.current = id
          queuedID.current = ""
          onReadMessage(id)
        }, 250)
    },
    [onReadMessage],
  )

  useEffect(() => {
    if (!readID.current && readThroughMessageID)
      readID.current = readThroughMessageID
  }, [readThroughMessageID])
  useEffect(() => {
    if (mode === "anchor" || switching || !readingActive) {
      if (readTimer.current) clearTimeout(readTimer.current)
      queuedID.current = ""
    }
  }, [mode, switching, readingActive])
  useEffect(
    () => () => {
      if (readTimer.current) clearTimeout(readTimer.current)
    },
    [],
  )

  useLayoutEffect(() => {
    if (!page || switching || mode !== "latest") return
    // 使用本次布局的实际位置；离底只统计上一窗口尾端之后的他人和系统消息。
    if (getAtBottom()) {
      newMessages.current.clear()
      setNewCount(0)
      if (readingActive && !page.hasLater) {
        const latest = page.messages[page.messages.length - 1]
        if (latest) queueRead(latest.id, true)
      }
    } else if (previousLast.current) {
      for (const message of page.messages) {
        if (
          compareConversationMessages(message, previousLast.current) > 0 &&
          (message.type === MessageType.MessageTypeSystem ||
            message.sender?.kind !== ChatSubjectKind.ChatSubjectKindOrganizationIdentity ||
            message.sender.sourceId !== identityID)
        ) newMessages.current.add(message.id)
      }
      setNewCount(newMessages.current.size)
    }
    previousLast.current = page.messages[page.messages.length - 1] ?? null
  }, [page, mode, switching, readingActive, atBottom, identityID, queueRead, getAtBottom])

  // 观察器回调读取本次渲染的窗口消息、本人身份与已读写入，窗口变化时不重建观察器。
  const latest = useRef({ messages: page?.messages ?? [], identityID, queueRead })
  latest.current = { messages: page?.messages ?? [], identityID, queueRead }
  const [reader] = useState(() => new MessageReadObserver(
    (entries) => {
      const { messages, identityID, queueRead } = latest.current
      for (const entry of entries) {
        const id = (entry.target as HTMLElement).dataset.messageId
        if (
          !id ||
          !entry.isIntersecting ||
          entry.intersectionRect.height <
            Math.min(entry.boundingClientRect.height / 2, 32)
        )
          continue
        seen.current.add(id)
        newMessages.current.delete(id)
      }
      setNewCount(newMessages.current.size)
      const index = messages.findIndex(
        (message) => message.id === readID.current,
      )
      if (readID.current && index < 0) return
      let nextID = readID.current
      for (const message of messages.slice(index + 1)) {
        if (
          (message.sender?.kind !== ChatSubjectKind.ChatSubjectKindOrganizationIdentity ||
            message.sender.sourceId !== identityID) &&
          !seen.current.has(message.id)
        )
          break
        nextID = message.id
      }
      if (nextID) queueRead(nextID)
    },
  ))

  useEffect(() => {
    reader.sync(
      conversationViewport(root.current),
      Boolean(page) && mode === "latest" && !switching && readingActive && Boolean(onReadMessage),
    )
  }, [reader, root, page, mode, switching, readingActive, onReadMessage])
  useEffect(() => () => reader.dispose(), [reader])

  return { newCount }
}
