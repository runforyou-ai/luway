/** 将输入区草稿保持在整个已登录工作台的生命周期内。 */
import { createContext, useContext, useState, useSyncExternalStore, type ReactNode } from "react"

import { ComposerDraftStore } from "@/lib/composer-draft-store"

const ComposerDraftContext = createContext<ComposerDraftStore | null>(null)

/** 为所有聊天页面提供同一份输入区草稿。 */
export function ComposerDraftProvider({ children }: { children: ReactNode }) {
  const [store] = useState(() => new ComposerDraftStore())
  return <ComposerDraftContext value={store}>{children}</ComposerDraftContext>
}

/** 读取输入区草稿存储。 */
export function useComposerDraftStore() {
  const store = useContext(ComposerDraftContext)
  if (!store) throw new Error("缺少 ComposerDraftProvider")
  return store
}

/** 订阅并读取一个会话的输入区草稿。 */
export function useComposerDraft(conversationKey: string) {
  const store = useComposerDraftStore()
  const drafts = useSyncExternalStore(store.subscribe, store.snapshot)
  return drafts.get(conversationKey)
}
