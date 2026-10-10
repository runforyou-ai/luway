/** 会话列表项的摘要数据与末条消息摘要文案，桌面端与移动端列表共用。 */
import type { TFunction } from "i18next"

import {
  ConversationStatus,
  ConversationSystemEventType,
  MessageType,
  MessageVisibility,
  isAgentInboxConversation,
  isServiceInboxConversation,
  isDirectInboxConversation,
  isGroupInboxConversation,
  type InboxConversation,
} from "@/api"

/** 返回会话列表项的摘要数据（末条消息、时间、未读等）；列表不支持的会话类型返回 null。 */
export function inboxConversationSummary(conversation: InboxConversation) {
  if (isServiceInboxConversation(conversation)) return conversation.service
  if (isAgentInboxConversation(conversation)) return conversation.agent
  if (isDirectInboxConversation(conversation)) return conversation.direct
  if (isGroupInboxConversation(conversation)) return conversation.group
  return null
}

/** 会话列表项的摘要文案：群解散、运行结果、不支持消息、AI 员工操作、服务进度与内部备注各有固定文案，其余取末条消息预览。 */
export function conversationPreview(
  conversation: InboxConversation,
  t: TFunction<"inbox">,
) {
  const summary = inboxConversationSummary(conversation)
  const groupDissolved =
    isGroupInboxConversation(conversation) &&
    conversation.group.status ===
      ConversationStatus.Archived
  const previewBody = groupDissolved
    ? t("groupDissolved")
    : conversation.lastMessageType === MessageType.AgentCancelled
      ? t("agentReplyStopped")
      : conversation.lastMessageType === MessageType.Unsupported
      ? t("unsupportedMessage")
      : conversation.lastMessageType === MessageType.AgentError
        ? t("agentRunFailed")
        : conversation.lastSystemEventType === ConversationSystemEventType.ConversationSystemEventAgentToolCallPending
          ? t("toolOperationPending")
          : conversation.lastSystemEventType === ConversationSystemEventType.ConversationSystemEventAgentToolCallResolved
            ? t("toolOperationResolved")
            : (isAgentInboxConversation(conversation) || isServiceInboxConversation(conversation)) &&
            conversation.lastMessageType === MessageType.System
          ? t("serviceStatusUpdated")
          : summary?.preview ||
          (isGroupInboxConversation(conversation) && summary?.lastMessageAt
            ? t("groupSystemUpdated")
            : t("messagesEmpty"))
  // 客户会话的末条消息是内部备注时，摘要标明来源。
  return isServiceInboxConversation(conversation) &&
    conversation.service.previewVisibility ===
      MessageVisibility.Internal
    ? t("previewInternalNote", { preview: previewBody })
    : previewBody
}
