/** 客户会话的 AI 写回复候选生成：读取可选 AI 员工，按防抖后的草稿与引用生成候选，并给出生成状态与无候选原因。 */
import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import {
  ServiceReplyMode,
  type ServiceReplyTone,
  generateServiceReplySuggestions,
  isApiError,
  listServiceReplyAgents,
} from "@/api"
import { selectServiceReplyAgentID } from "@/features/inbox/composer/customer-reply-agent"
import { useCustomerTranslation } from "@/features/inbox/shared/customer-translation"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDebouncedValue } from "@/hooks/use-debounced-value"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"

const replySourceDebounceDelay = 600

/** 返回弹层打开期间的候选生成状态；failed 表示无候选原因来自读取或生成失败。 */
export function useReplySuggestions({
  conversationID,
  open,
  disabled,
  mode,
  tone,
  preferredAgentID,
  draft,
  replyToMessageID,
}: {
  conversationID: string
  open: boolean
  disabled: boolean
  mode: ServiceReplyMode
  tone: ServiceReplyTone
  preferredAgentID: string
  draft: string
  replyToMessageID: string
}) {
  const { t } = useTranslation("inbox")
  // 打开时以当前草稿和引用作为生成条件，打开期间变化防抖后才成为新的生成条件。
  const current = useMemo(() => ({ draft, replyToMessageID }), [draft, replyToMessageID])
  const source = useDebouncedValue(current, replySourceDebounceDelay, !open)

  const agentOptions = useResource(
    resourceKeys.serviceReplyAgents(),
    listServiceReplyAgents,
    { enabled: open, staleTime: 0 },
  )
  const agents = agentOptions.data ?? []
  const agentIdentityID = selectServiceReplyAgentID(
    agents,
    preferredAgentID,
  )
  const rewrite = mode === ServiceReplyMode.Rewrite
  // 翻译发送时候选按本人语言书写，发送时再译为客户语言。
  const customerTranslation = useCustomerTranslation()
  const replyLanguage =
    customerTranslation?.replyNeedsTranslation && customerTranslation.translateReply
      ? customerTranslation.state.viewerLanguage
      : ""
  const parameters = {
    agentIdentityId: agentIdentityID,
    mode,
    tone,
    draft: rewrite ? source.draft.trim() : "",
    replyToMessageId: source.replyToMessageID,
    language: replyLanguage,
  }
  const rewriteEmpty = rewrite && draft.trim() === ""
  const available =
    open && !disabled && agentIdentityID !== "" && !rewriteEmpty
  const ready = available && (!rewrite || parameters.draft !== "")
  // 生成条件落后于当前草稿或引用时，隐藏旧结果并按生成中处理。
  const sourceCurrent =
    source.replyToMessageID === replyToMessageID &&
    (!rewrite || source.draft === draft)
  const suggestions = useResource(
    resourceKeys.serviceReplySuggestions(conversationID, parameters),
    () => generateServiceReplySuggestions(conversationID, parameters),
    { enabled: ready, staleTime: Infinity, refetchOnWindowFocus: false },
  )
  const generating =
    agentOptions.loading ||
    (available &&
      (!sourceCurrent || suggestions.loading || suggestions.refreshing))
  // 只在可生成且未失败时展示候选。
  const candidates = ready && !suggestions.error ? (suggestions.data?.candidates ?? []) : []
  // 没有候选时的提示按原因排序：员工读取、可用员工、改写草稿和生成失败。
  const emptyMessage = agentOptions.error
    ? t("replyAssistantAgentsLoadError")
    : agents.length === 0
      ? t("agentPickerEmpty")
      : rewriteEmpty
        ? t("replyAssistantRewriteEmpty")
        : suggestions.error
          ? isApiError(suggestions.error)
            ? apiErrorMessage(suggestions.error)
            : t("replyAssistantError")
          : t("replyAssistantEmpty")

  return {
    agents,
    agentIdentityID,
    ready,
    generating,
    candidates,
    emptyMessage,
    failed: Boolean(agentOptions.error || (ready && suggestions.error)),
    refresh: suggestions.refresh,
  }
}
