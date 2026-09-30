/** 在已登录工作台内按会话保存输入区未发送的草稿。 */
import type { ConversationMessageReference, MessageVisibility } from "@/api"
import type { MentionAllToken } from "@/lib/mention-token"
import type { MentionTarget } from "@/lib/outgoing-message-store"

/** 一种输入模式下的正文与提醒标记。 */
export type ComposerModeDraft = {
  body: string
  mentions: MentionTarget[]
  mentionAllToken: MentionAllToken | null
}

/** 一个会话的输入区草稿：当前输入模式、各模式正文与引用目标。 */
type ConversationComposerDraft = {
  visibility: MessageVisibility
  modes: Partial<Record<MessageVisibility, ComposerModeDraft>>
  replyTargets: Partial<Record<MessageVisibility, ConversationMessageReference | null>>
}

/** 按会话保存输入区草稿，正文与引用目标都为空的草稿不保留。 */
export class ComposerDraftStore {
  private drafts = new Map<string, ConversationComposerDraft>()
  private listeners = new Set<() => void>()

  /** 订阅草稿变化。 */
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /** 返回当前按会话分组的草稿快照。 */
  snapshot = () => this.drafts

  /** 读取会话草稿。 */
  get(conversationKey: string) {
    return this.drafts.get(conversationKey)
  }

  /** 删除会话草稿。 */
  remove(conversationKey: string) {
    if (!this.drafts.has(conversationKey)) return
    this.drafts = new Map(this.drafts)
    this.drafts.delete(conversationKey)
    for (const listener of this.listeners) listener()
  }

  /** 按字段写入会话草稿，patch 中的字段整体替换原值，写入后没有内容时删除。 */
  update(conversationKey: string, patch: Partial<ConversationComposerDraft>, fallbackVisibility: MessageVisibility) {
    if (!conversationKey) return
    const current = this.drafts.get(conversationKey)
    const next: ConversationComposerDraft = {
      visibility: fallbackVisibility,
      modes: {},
      replyTargets: {},
      ...current,
      ...patch,
    }
    const hasContent =
      Object.values(next.modes).some((mode) => mode?.body.trim()) ||
      Object.values(next.replyTargets).some(Boolean)
    if (!hasContent && !current) return
    this.drafts = new Map(this.drafts)
    if (hasContent) this.drafts.set(conversationKey, next)
    else this.drafts.delete(conversationKey)
    for (const listener of this.listeners) listener()
  }
}

/** 返回会话草稿中可在列表展示的正文，优先当前输入模式。 */
export function draftPreview(draft: ConversationComposerDraft | undefined) {
  if (!draft) return ""
  const current = draft.modes[draft.visibility]?.body.trim()
  if (current) return current
  return Object.values(draft.modes).find((mode) => mode?.body.trim())?.body.trim() ?? ""
}
