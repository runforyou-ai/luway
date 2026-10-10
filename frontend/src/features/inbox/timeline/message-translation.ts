/** 时间线消息的译文：进入可视区附近时按需翻译，并在原文与译文之间切换。 */
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react"

import { MessageType, MessageVisibility, translateConversationMessage } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { readableLanguage, sameLanguage } from "@/lib/languages"

import { useCustomerTranslation } from "@/features/inbox/shared/customer-translation"
import type { TimelineMessage } from "./timeline-messages"

// 同一滚动区域内的消息行共用一个观察器，可视区上下各预留一屏，滚动到达前完成翻译。
const nearViewportObservers = new WeakMap<Element, { observer: IntersectionObserver; callbacks: Map<Element, () => void> }>()

/** 行进入所在滚动区域可视范围附近时调用 onNear，返回取消观察的函数。 */
function observeNearViewport(row: HTMLElement, onNear: () => void) {
  const viewport = row.closest<HTMLElement>('[data-slot="scroll-area-viewport"]')
  const key = viewport ?? document.documentElement
  let shared = nearViewportObservers.get(key)
  if (!shared) {
    const callbacks = new Map<Element, () => void>()
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) if (entry.isIntersecting) callbacks.get(entry.target)?.()
      },
      { root: viewport, rootMargin: "100% 0px" },
    )
    shared = { observer, callbacks }
    nearViewportObservers.set(key, shared)
  }
  const { observer, callbacks } = shared
  callbacks.set(row, onNear)
  observer.observe(row)
  return () => {
    callbacks.delete(row)
    observer.unobserve(row)
  }
}

/** 一条消息的译文展示状态。 */
export type MessageTranslationView =
  | { status: "none" }
  | { status: "pending" }
  | { status: "failed"; retry: () => void }
  | { status: "translated"; language: string; showingOriginal: boolean; toggle: () => void }

/** 返回消息的译文展示状态与气泡应显示的正文；fromCustomer 表示消息由客户发出。 */
export function useMessageTranslation(message: TimelineMessage, fromCustomer: boolean, rowRef: RefObject<HTMLElement | null>) {
  const translation = useCustomerTranslation()
  const [nearViewport, setNearViewport] = useState(false)
  const [toggled, setToggled] = useState(false)
  const viewerLanguage = translation?.state.viewerLanguage ?? ""
  // 只翻译客户会话中有正文的对客文本与附件说明。
  const eligible =
    translation !== null &&
    message.body !== "" &&
    message.visibility === MessageVisibility.Shared &&
    (message.type === MessageType.Text || message.type === MessageType.Attachment)
  const stored = eligible && message.translation && sameLanguage(message.translation.language, viewerLanguage)
    ? message.translation
    : null
  const readable = message.language !== "" && readableLanguage(message.language, viewerLanguage)
  // 已保存的消息尚无本人语言的译文且无法直接阅读时，进入可视区附近再翻译。
  const needsFetch = eligible && !stored && !readable && !message.local

  useEffect(() => {
    const row = rowRef.current
    if (!needsFetch || nearViewport || !row) return
    return observeNearViewport(row, () => setNearViewport(true))
  }, [needsFetch, nearViewport, rowRef])

  const fetched = useResource(
    resourceKeys.messageTranslation(translation?.conversationID ?? "", { messageId: message.id, language: viewerLanguage }),
    () => translateConversationMessage(translation?.conversationID ?? "", message.id),
    { enabled: needsFetch && nearViewport, staleTime: Infinity, refetchOnWindowFocus: false },
  )
  const result = needsFetch ? fetched.data : undefined

  // 客户消息识别出新语言时交由翻译上下文判断是否刷新回复语言。
  const detectedLanguage = fromCustomer ? (result?.language ?? "") : ""
  const noteCustomerLanguage = translation?.noteCustomerLanguage
  useEffect(() => {
    if (detectedLanguage !== "" && detectedLanguage !== "und") noteCustomerLanguage?.(message.id, detectedLanguage)
  }, [detectedLanguage, message.id, noteCustomerLanguage])

  let view: MessageTranslationView = { status: "none" }
  let translatedBody = ""
  if (stored) {
    translatedBody = stored.body
  } else if (needsFetch) {
    // 服务端不翻译的消息返回 null，按无需翻译处理；返回了结果但没有译文且无法直接阅读时为翻译失败。
    if (result?.body) translatedBody = result.body
    else if (fetched.error || (result && !readableLanguage(result.language, viewerLanguage)))
      view = { status: "failed", retry: () => void fetched.refresh() }
    else if (result === undefined) view = { status: "pending" }
  }
  const showingOriginal = (translation?.showOriginal ?? false) !== toggled
  if (translatedBody) {
    view = {
      status: "translated",
      language: stored ? message.language : (result?.language ?? message.language),
      showingOriginal,
      toggle: () => setToggled((value) => !value),
    }
  }
  const body = translatedBody && !showingOriginal ? translatedBody : message.body

  // 可视区上方的消息因切换译文改变高度时，保持当前阅读位置。
  const heightRef = useRef<number | null>(null)
  useLayoutEffect(() => {
    const row = rowRef.current
    if (!row) return
    const height = row.getBoundingClientRect().height
    const previous = heightRef.current
    heightRef.current = height
    if (previous === null || previous === height) return
    const viewport = row.closest<HTMLElement>('[data-slot="scroll-area-viewport"]')
    if (viewport && row.getBoundingClientRect().top < viewport.getBoundingClientRect().top)
      viewport.scrollTop += height - previous
  }, [body, rowRef])

  return { view, body }
}
