/** 消息输入框的 @ 提醒：候选查询、结构化提醒目标、所有人标记与候选键盘导航。 */
import {
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type RefObject,
} from "react"
import type { UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"

import { WorkspaceIdentityType, type GroupParticipant, type MemberOption } from "@/api"
import type { ConversationComposerValues } from "@/features/inbox/composer/conversation-composer-schema"
import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import type { ComposerModeDraft } from "@/features/inbox/state/composer-draft-store"
import { mentionTokenPattern, reconcileMentionAllToken, type MentionAllToken } from "@/lib/mention-token"
import type { MentionTarget } from "@/features/inbox/state/outgoing-message-store"

import { resizeComposerInput } from "./composer-input"

/** @ 候选项：所有人或一名可提醒的成员；label 是候选列表中的展示名，displayName 是插入正文的姓名。 */
export type MentionCandidate =
  | { kind: "all"; displayName: string; label: string }
  | { kind: "member"; displayName: string; label: string; target: MentionTarget }

/** 统计正文中仍然存在的完整 @ 姓名标记。 */
function countMentionTokens(body: string, displayName: string) {
  return Array.from(
    body.matchAll(new RegExp(mentionTokenPattern([displayName]), "gu")),
  ).length
}

/** 管理输入框的 @ 候选与结构化提醒状态，initialDraft 为恢复的会话草稿。 */
export function useComposerMentions({
  initialDraft,
  form,
  inputRef,
  typingReport,
  groupConversation,
  customerConversation,
  internalNote,
  groupParticipants,
  noteMentionMembers,
  currentIdentityID,
  noteSwitchAvailable,
}: {
  initialDraft: ComposerModeDraft | undefined
  form: UseFormReturn<ConversationComposerValues>
  inputRef: RefObject<HTMLTextAreaElement | null>
  typingReport: { input: (value: string) => void }
  groupConversation: boolean
  customerConversation: boolean
  internalNote: boolean
  groupParticipants?: GroupParticipant[]
  noteMentionMembers?: MemberOption[]
  currentIdentityID: string
  noteSwitchAvailable: boolean
}) {
  const { t } = useTranslation("inbox")
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const [mentions, setMentions] = useState<MentionTarget[]>(() => initialDraft?.mentions ?? [])
  const mentionsRef = useRef(mentions)
  mentionsRef.current = mentions
  const [mentionAllToken, setMentionAllToken] =
    useState<MentionAllToken | null>(() => initialDraft?.mentionAllToken ?? null)
  const mentionAll = mentionAllToken !== null
  const [mentionQuery, setMentionQuery] = useState<{
    start: number
    value: string
  } | null>(null)
  const [activeMentionIndex, setActiveMentionIndex] = useState(0)

  // 群聊提醒当前成员，个人 AI 员工按「负责人的 AI 员工 · 名称」展示；客户会话的内部备注提醒企业真人成员。
  const mentionOptions = useMemo<{ target: MentionTarget; label: string }[]>(() => {
    if (groupConversation) {
      return (groupParticipants ?? []).map((participant) => ({
        target: {
          identityID: participant.identityId,
          chatSubjectID: participant.chatSubjectId,
          displayName: participant.displayName,
        },
        label: personalAgentDisplayName(participant.displayName, participant.personalResponsibleName),
      }))
    }
    if (!customerConversation || !internalNote) return []
    return (noteMentionMembers ?? [])
      .filter((member) => member.type === WorkspaceIdentityType.User)
      .map((member) => ({
        target: { identityID: member.id, chatSubjectID: null, displayName: member.displayName },
        label: member.displayName,
      }))
  }, [personalAgentDisplayName, customerConversation, groupConversation, groupParticipants, internalNote, noteMentionMembers])

  const mentionCandidates = useMemo<MentionCandidate[]>(() => {
    if (!mentionQuery) return []
    const query = mentionQuery.value.toLocaleLowerCase()
    const candidates: MentionCandidate[] = []
    if (groupConversation && !mentionAll && t("messageMentionAll").toLocaleLowerCase().includes(query)) {
      candidates.push({ kind: "all", displayName: t("messageMentionAll"), label: t("messageMentionAll") })
    }
    candidates.push(
      ...mentionOptions
        .filter(
          ({ target, label }) =>
            target.identityID !== currentIdentityID &&
            !mentions.some((mention) => mention.identityID === target.identityID) &&
            label.toLocaleLowerCase().includes(query),
        )
        .map(({ target, label }) => ({
          kind: "member" as const,
          displayName: target.displayName,
          label,
          target,
        })),
    )
    return candidates.slice(0, 8)
  }, [
    currentIdentityID,
    groupConversation,
    mentionOptions,
    mentionQuery,
    mentions,
    mentionAll,
    t,
  ])
  // 对客模式输入 @ 时提示切换到内部备注提醒同事。
  const noteMentionHint =
    customerConversation && !internalNote && noteSwitchAvailable && mentionQuery !== null

  /** 根据光标前文本更新 @ 候选查询。 */
  function updateMentionQuery(value: string, selectionStart: number | null) {
    if (
      (!groupConversation && !customerConversation) ||
      selectionStart === null
    ) {
      setMentionQuery(null)
      return
    }
    const beforeCaret = value.slice(0, selectionStart)
    const match = beforeCaret.match(/(?:^|\s)@([^\s@]*)$/)
    if (!match) {
      setMentionQuery(null)
      return
    }
    const markerOffset = match[0].lastIndexOf("@")
    setMentionQuery({
      start: beforeCaret.length - match[0].length + markerOffset,
      value: match[1],
    })
    setActiveMentionIndex(0)
  }

  /** 删除正文中已经不存在的结构化提醒目标。 */
  function reconcileMentions(value: string) {
    setMentions((current) => {
      const remainingByName = new Map<string, number>()
      return current.filter((mention) => {
        const remaining =
          remainingByName.get(mention.displayName) ??
          countMentionTokens(value, mention.displayName)
        remainingByName.set(mention.displayName, Math.max(remaining - 1, 0))
        return remaining > 0
      })
    })
  }

  /** 在正文光标处插入选中的成员或所有人标记。 */
  function selectMention(candidate: MentionCandidate) {
    const query = mentionQuery
    const input = inputRef.current
    if (!query || !input) return
    const body = form.getValues("body")
    const caret = input.selectionStart ?? body.length
    const token = `@${candidate.displayName} `
    const nextBody = `${body.slice(0, query.start)}${token}${body.slice(caret)}`
    const nextCaret = query.start + token.length
    form.setValue("body", nextBody, { shouldDirty: true })
    typingReport.input(nextBody)
    if (candidate.kind === "all") {
      setMentionAllToken({ start: query.start, text: token.trimEnd() })
    } else {
      setMentionAllToken((current) =>
        reconcileMentionAllToken(current, body, nextBody, nextCaret),
      )
      const { target } = candidate
      setMentions((current) =>
        current.some((mention) => mention.identityID === target.identityID)
          ? current
          : [...current, target],
      )
    }
    setMentionQuery(null)
    window.requestAnimationFrame(() => {
      input.focus()
      input.setSelectionRange(nextCaret, nextCaret)
      resizeComposerInput(input)
    })
  }

  /** 候选列表可见时处理上下选择、Enter 或 Tab 选中与 Escape 关闭，返回按键是否已处理。 */
  function handleMentionKeyDown(
    event: KeyboardEvent<HTMLTextAreaElement>,
    composing: boolean,
  ) {
    if (composing || !mentionQuery || mentionCandidates.length === 0) return false
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault()
      const direction = event.key === "ArrowDown" ? 1 : -1
      setActiveMentionIndex((current) =>
        (current + direction + mentionCandidates.length) %
        mentionCandidates.length,
      )
      return true
    }
    if (event.key === "Enter" || (event.key === "Tab" && !event.shiftKey)) {
      event.preventDefault()
      selectMention(
        mentionCandidates[
          Math.min(activeMentionIndex, mentionCandidates.length - 1)
        ],
      )
      return true
    }
    if (event.key === "Escape") {
      event.preventDefault()
      setMentionQuery(null)
      return true
    }
    return false
  }

  return {
    mentions,
    setMentions,
    mentionsRef,
    mentionAllToken,
    setMentionAllToken,
    mentionAll,
    mentionQuery,
    setMentionQuery,
    activeMentionIndex,
    mentionCandidates,
    noteMentionHint,
    updateMentionQuery,
    reconcileMentions,
    selectMention,
    handleMentionKeyDown,
  }
}
