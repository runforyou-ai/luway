/** 客户会话的翻译上下文：翻译状态、整段显示原文、发送时翻译与回复语言。 */
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react"

import { getConversationTranslation, updateCustomerReplyLanguage, type ConversationTranslation } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { sameLanguage } from "@/lib/languages"

/** 客户会话翻译上下文的值。 */
type CustomerTranslationValue = {
  conversationID: string
  state: ConversationTranslation
  /** 客户语言未知或与本人语言不同，对客回复需要翻译。 */
  replyNeedsTranslation: boolean
  /** 本次会话中对客回复是否翻译后发送。 */
  translateReply: boolean
  setTranslateReply: (value: boolean) => void
  /** 时间线是否整体显示原文。 */
  showOriginal: boolean
  setShowOriginal: (value: boolean) => void
  /** 锁定回复语言，空字符串表示跟随客户最近消息的语言。 */
  setReplyLanguage: (language: string) => Promise<void>
  /** 重新读取翻译状态。 */
  refresh: () => void
  /** 客户消息识别出与当前客户语言不同的语言时刷新翻译状态，每条消息只触发一次。 */
  noteCustomerLanguage: (messageID: string, language: string) => void
}

const CustomerTranslationContext = createContext<CustomerTranslationValue | null>(null)

/** 为客户会话读取翻译状态并提供翻译上下文；企业未设置翻译模型时不提供。 */
export function CustomerTranslationProvider({ conversationID, children }: { conversationID: string | null; children: ReactNode }) {
  const invalidate = useResourceInvalidator()
  const [translateReply, setTranslateReply] = useState(true)
  const [showOriginal, setShowOriginal] = useState(false)
  const notedRef = useRef(new Set<string>())
  const translation = useResource(
    resourceKeys.conversationTranslation(conversationID ?? ""),
    () => getConversationTranslation(conversationID ?? ""),
    { enabled: Boolean(conversationID), staleTime: 0 },
  )
  const state = translation.data
  const refresh = useCallback(() => {
    if (conversationID) void invalidate(resourceKeys.conversationTranslation(conversationID))
  }, [conversationID, invalidate])
  const customerLanguage = state?.customerLanguage ?? ""
  const replyLanguageLocked = state?.replyLanguageLocked ?? false
  const noteCustomerLanguage = useCallback((messageID: string, language: string) => {
    if (replyLanguageLocked || sameLanguage(language, customerLanguage) || notedRef.current.has(messageID)) return
    notedRef.current.add(messageID)
    refresh()
  }, [customerLanguage, refresh, replyLanguageLocked])
  const value = useMemo<CustomerTranslationValue | null>(
    () =>
      conversationID && state?.enabled
        ? {
            conversationID,
            state,
            replyNeedsTranslation:
              state.customerLanguage === "" || !sameLanguage(state.customerLanguage, state.viewerLanguage),
            translateReply,
            setTranslateReply,
            showOriginal,
            setShowOriginal,
            setReplyLanguage: async (language) => {
              await updateCustomerReplyLanguage(conversationID, { language })
              refresh()
            },
            refresh,
            noteCustomerLanguage,
          }
        : null,
    [conversationID, noteCustomerLanguage, refresh, showOriginal, state, translateReply],
  )
  return <CustomerTranslationContext.Provider value={value}>{children}</CustomerTranslationContext.Provider>
}

/** 返回当前客户会话的翻译上下文，不在客户会话或未开启翻译时为 null。 */
export function useCustomerTranslation() {
  return useContext(CustomerTranslationContext)
}
