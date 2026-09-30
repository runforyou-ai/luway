/** 对客回复与内部备注两种输入模式各自保留正文和提醒成员，切换模式时保存并载入对应草稿。 */
import {
  useEffect,
  useState,
  useRef,
  type Dispatch,
  type RefObject,
  type SetStateAction,
} from "react"
import type { UseFormReturn } from "react-hook-form"

import type { MessageVisibility } from "@/api"
import type { ConversationComposerValues } from "@/features/inbox/conversation-composer-schema"
import type { ComposerModeDraft } from "@/lib/composer-draft-store"
import type { MentionTarget } from "@/lib/outgoing-message-store"

import { resizeComposerInput } from "./composer-input"

/** 按可见范围保存和载入输入框草稿，并提供切换模式的入口；initialModes 为会话草稿中各模式的内容。 */
export function useVisibilityDrafts({
  initialModes,
  form,
  visibility,
  onVisibilityChange,
  inputRef,
  mentionsRef,
  setMentions,
  closeMentionQuery,
}: {
  initialModes: Partial<Record<MessageVisibility, ComposerModeDraft>> | undefined
  form: UseFormReturn<ConversationComposerValues>
  visibility: MessageVisibility
  onVisibilityChange?: (visibility: MessageVisibility) => void
  inputRef: RefObject<HTMLTextAreaElement | null>
  mentionsRef: RefObject<MentionTarget[]>
  setMentions: Dispatch<SetStateAction<MentionTarget[]>>
  closeMentionQuery: () => void
}) {
  // 当前模式的草稿由输入框承载，其余模式的初始草稿取自会话草稿。
  const [otherModes] = useState(
    () => Object.entries(initialModes ?? {}).filter(([mode]) => mode !== visibility) as [MessageVisibility, ComposerModeDraft][],
  )
  const draftsRef = useRef<Partial<Record<MessageVisibility, string>>>(
    Object.fromEntries(otherModes.map(([mode, draft]) => [mode, draft.body])),
  )
  const draftMentionsRef = useRef<Partial<Record<MessageVisibility, MentionTarget[]>>>(
    Object.fromEntries(otherModes.map(([mode, draft]) => [mode, draft.mentions])),
  )
  const appliedVisibilityRef = useRef(visibility)
  const focusAfterSwitchRef = useRef(false)

  // 页签切换和引用、填入回复引起的模式变化共用同一套草稿保存与载入。
  useEffect(() => {
    const previous = appliedVisibilityRef.current
    if (previous === visibility) return
    appliedVisibilityRef.current = visibility
    draftsRef.current[previous] = form.getValues("body")
    draftMentionsRef.current[previous] = mentionsRef.current
    form.setValue("body", draftsRef.current[visibility] ?? "")
    setMentions(draftMentionsRef.current[visibility] ?? [])
    closeMentionQuery()
    delete draftsRef.current[visibility]
    delete draftMentionsRef.current[visibility]
    const focus = focusAfterSwitchRef.current
    focusAfterSwitchRef.current = false
    window.requestAnimationFrame(() => {
      resizeComposerInput(inputRef.current)
      if (focus) form.setFocus("body")
    })
  }, [form, visibility])

  /** 切换输入模式并把焦点留在输入框。 */
  function switchVisibility(next: MessageVisibility) {
    if (next === visibility) return
    focusAfterSwitchRef.current = true
    onVisibilityChange?.(next)
  }

  /** 返回当前模式以外各模式的草稿。 */
  function inactiveModes() {
    return Object.fromEntries(
      Object.entries(draftsRef.current).map(([mode, body]) => [
        mode,
        { body: body ?? "", mentions: draftMentionsRef.current[mode as MessageVisibility] ?? [], mentionAllToken: null },
      ]),
    ) as Partial<Record<MessageVisibility, ComposerModeDraft>>
  }

  return { draftsRef, focusAfterSwitchRef, switchVisibility, inactiveModes }
}
