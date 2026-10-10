/** 会话时间线的消息模型：合并服务端窗口与即时发送项，并提供发送者与提醒名称。 */
import { ChatSubjectKind, MessageType, type ConversationMessage } from "@/api"
import englishMention from "@/i18n/locales/en-US/mention"
import chineseMention from "@/i18n/locales/zh-CN/mention"
import type { SupportedLanguage } from "@/i18n/resources"
import {
  coveredByWindow,
  windowCoverage,
  type OutgoingConversationDraft,
  type OutgoingConversationMessage,
} from "@/features/inbox/state/outgoing-message-store"

import { compareConversationMessages } from "./conversation-window"

/** 时间线中的一条消息，本地发送项与服务端消息共用该结构。 */
export type TimelineMessage = Pick<
  ConversationMessage,
  | "id"
  | "type"
  | "visibility"
  | "body"
  | "language"
  | "translation"
  | "attachment"
  | "originatedAt"
  | "sender"
  | "sessionStart"
  | "systemEvent"
  | "replyTo"
  | "canReply"
  | "canNoteReply"
  | "mentions"
  | "mentionAll"
  | "agentProcess"
  | "agentErrorCode"
  | "localAgentReply"
  | "localAgentTurns"
  | "delivery"
> & {
  persistedMessageID: string | null
  clientMessageID: string | null
  draftMentions: OutgoingConversationDraft["mentions"]
  mentionAllToken: OutgoingConversationDraft["mentionAllToken"]
  local: boolean
  deliveryStatus: "sending" | "failed" | null
}

// 按全部支持语言收集“所有人”的名称，新增语言时由类型检查要求补齐。
const mentionAllNamesByLanguage: Record<SupportedLanguage, string> = {
  "zh-CN": chineseMention.all,
  "en-US": englishMention.all,
}
const mentionAllNames = Object.values(mentionAllNamesByLanguage)

/** 返回视觉分组使用的稳定发送者标识。 */
export function timelineSenderKey(
  message: TimelineMessage,
  currentIdentityID: string,
) {
  if (message.local) {
    return `${ChatSubjectKind.WorkspaceIdentity}:${currentIdentityID}`
  }
  if (!message.sender) return `unknown:${message.id}`
  return `${message.sender.kind}:${message.sender.sourceId}`
}

// 服务端消息与发送项各自缓存时间线结构，来源对象不变时复用同一引用。
const persistedTimelineMessages = new WeakMap<ConversationMessage, TimelineMessage>()
const outgoingTimelineMessages = new WeakMap<OutgoingConversationMessage, TimelineMessage>()

/** 把服务端消息转成时间线消息。 */
function persistedTimelineMessage(message: ConversationMessage): TimelineMessage {
  let cached = persistedTimelineMessages.get(message)
  if (!cached) {
    cached = {
      ...message,
      persistedMessageID: message.id,
      clientMessageID: null,
      draftMentions: [],
      mentionAllToken: null,
      local: false,
      deliveryStatus: null,
    }
    persistedTimelineMessages.set(message, cached)
  }
  return cached
}

/** 把即时发送项转成本地时间线消息。 */
function outgoingTimelineMessage(message: OutgoingConversationMessage): TimelineMessage {
  let cached = outgoingTimelineMessages.get(message)
  if (!cached) {
    cached = {
      id: `local:${message.clientMessageID}`,
      persistedMessageID: message.saved?.id ?? null,
      type: message.saved?.type ?? (message.attachment ? MessageType.Attachment : MessageType.Text),
      visibility: message.saved?.visibility ?? message.visibility,
      attachment: message.saved?.attachment ?? message.attachment ?? null,
      // 翻译发送的结果以客户收到的正文为原文，客服书写的原话作为本人语言的译文。
      body: message.saved?.body ?? message.body,
      language: message.saved?.language ?? "",
      translation: message.saved?.translation ?? null,
      originatedAt: message.originatedAt,
      sender: null,
      agentProcess: null,
      agentErrorCode: null,
      localAgentReply: null,
      localAgentTurns: [],
      delivery: message.saved?.delivery ?? null,
      sessionStart: null,
      systemEvent: null,
      replyTo: message.replyTo,
      canReply: false,
      canNoteReply: false,
      mentions: message.mentions.map((mention) => ({
        chatSubjectId: mention.chatSubjectID ?? "",
        kind: ChatSubjectKind.WorkspaceIdentity,
        sourceId: mention.identityID,
        displayName: mention.displayName,
      })),
      clientMessageID: message.clientMessageID,
      draftMentions: message.mentions,
      mentionAll: message.mentionAll,
      mentionAllToken: message.mentionAllToken,
      local: true,
      deliveryStatus:
        message.status === "failed"
          ? "failed"
          : message.showSending && !message.saved
            ? "sending"
            : null,
    }
    outgoingTimelineMessages.set(message, cached)
  }
  return cached
}

/** 合并服务端消息和当前页面的即时发送状态。 */
export function mergeTimelineMessages(
  current: ConversationMessage[],
  outgoing: OutgoingConversationMessage[],
) {
  const messages = [...current].sort(compareConversationMessages).map(persistedTimelineMessage)
  const coverage = windowCoverage(current)
  for (const message of outgoing) {
    // 只为窗口之外的发送项生成本地气泡。
    if (!coveredByWindow(message, coverage)) messages.push(outgoingTimelineMessage(message))
  }
  // 服务端消息只来自连续窗口，尚未补入窗口的发送结果继续作为本地项目展示。
  return messages
}

/** 收集一条消息中结构化提醒的成员名称，长名称优先匹配。 */
export function messageMentionNames(message: TimelineMessage) {
  return [
    ...message.mentions.map((mention) => mention.displayName?.trim() ?? ""),
    ...(message.mentionAll ? mentionAllNames : []),
  ]
    .filter((name, index, values) => name && values.indexOf(name) === index)
    .sort((left, right) => right.length - left.length)
}
