/** 将成员消息发送状态保持在整个已登录工作台的生命周期内。 */
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

import type { ConversationMessage } from "@/api"
import { useKeyedSnapshot } from "@/features/inbox/state/keyed-listeners"
import {
  OutgoingMessageStore,
  type OutgoingConversationDraft,
  type OutgoingConversationMessage,
} from "@/features/inbox/state/outgoing-message-store"

const OutgoingMessageContext = createContext<OutgoingMessageStore | null>(null)

/** 为所有聊天页面提供同一份发送状态。 */
export function OutgoingMessageProvider({ children }: { children: ReactNode }) {
  const [store] = useState(() => new OutgoingMessageStore())
  useEffect(() => () => store.dispose(), [store])
  return (
    <OutgoingMessageContext value={store}>{children}</OutgoingMessageContext>
  )
}

/** 读取发送状态存储，用于草稿移交、窗口收尾和失权清理。 */
export function useOutgoingMessageStore() {
  const store = useContext(OutgoingMessageContext)
  if (!store) throw new Error("缺少 OutgoingMessageProvider")
  return store
}

/** 读取会话的发送项并绑定发送入口，同时接管该会话对应草稿的发送项。 */
export function useOutgoingMessages(
  conversationID: string,
  draftConversationID = "",
) {
  const store = useOutgoingMessageStore()
  const current = useKeyedSnapshot(store.subscribe, conversationID, store.messages)
  // 没有草稿会话时不读取草稿发送项。
  const drafted = useKeyedSnapshot(store.subscribe, draftConversationID, (key) =>
    key ? store.messages(key) : emptyMessages,
  )
  useEffect(() => {
    if (!conversationID || !draftConversationID) return
    // 进入正式会话后移交该草稿的发送项。
    store.adopt(draftConversationID, conversationID)
  }, [conversationID, draftConversationID, store])
  const messages = useMemo(
    () => (drafted.length ? [...current, ...drafted] : current),
    [current, drafted],
  )
  const target = conversationID || draftConversationID
  return useMemo(
    () => ({
      messages,
      /** 在当前会话登记一次发送，尚无会话编号时登记到草稿。 */
      start: (draft: OutgoingConversationDraft) => store.start(target, draft),
      /** 按发送逻辑编号写入发送结果。 */
      succeed: (clientMessageID: string, saved: ConversationMessage) =>
        store.succeed(clientMessageID, saved),
      /** 按发送逻辑编号写入发送失败，保留手动重试。 */
      fail: (clientMessageID: string) => store.fail(clientMessageID),
      /** 按发送逻辑编号移除一条发送项。 */
      discard: (clientMessageID: string) => store.discard(clientMessageID),
    }),
    [messages, store, target],
  )
}

const emptyMessages: OutgoingConversationMessage[] = []
