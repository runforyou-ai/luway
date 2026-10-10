/** 按会话类型调用对应的成员文本消息发送接口。 */
import {
  ConversationType,
  MessageVisibility,
  sendAgentTextMessage,
  sendServiceCopilotTextMessage,
  sendServiceTextMessage,
  sendDirectTextMessage,
  sendGroupTextMessage,
  type ConversationMessage,
  type CustomerReplyTranslation,
  type DirectTextMessageInput,
} from "@/api"
import type { MentionTarget } from "@/features/inbox/state/outgoing-message-store"

/** 按会话类型发送一条成员文本消息；处理方查看服务会话时走服务会话发送，单聊类草稿首发走调用方提供的入口。 */
export function sendComposerTextMessage({
  conversationType,
  service,
  conversationID,
  clientMessageID,
  body,
  replyToMessageID,
  visibility,
  mentions,
  mentionAll,
  translate,
  translation,
  sendIndividualMessage,
}: {
  conversationType: ConversationType
  service: boolean
  conversationID: string
  clientMessageID: string
  body: string
  replyToMessageID: string
  visibility: MessageVisibility
  mentions: MentionTarget[]
  mentionAll: boolean
  /** 对客回复是否译为客户语言发送，translation 为预览过的译文。 */
  translate: boolean
  translation: CustomerReplyTranslation | null
  sendIndividualMessage?: (
    input: DirectTextMessageInput,
  ) => Promise<ConversationMessage>
}): Promise<ConversationMessage> {
  const messageInput = { clientMessageId: clientMessageID, body }
  if (service)
    return sendServiceTextMessage(conversationID, {
      ...messageInput,
      replyToMessageId: replyToMessageID,
      visibility,
      mentionIdentityIds: visibility === MessageVisibility.Internal
        ? mentions.map((mention) => mention.identityID)
        : [],
      translate: translate && visibility === MessageVisibility.Shared,
      translation: visibility === MessageVisibility.Shared ? translation : null,
    })
  switch (conversationType) {
    case ConversationType.Agent:
    case ConversationType.Copilot:
    case ConversationType.Direct: {
      const directInput = {
        ...messageInput,
        replyToMessageId: replyToMessageID,
      }
      // 草稿首发走调用方入口，已有会话按类型发送。
      if (sendIndividualMessage) return sendIndividualMessage(directInput)
      if (conversationType === ConversationType.Agent)
        return sendAgentTextMessage(conversationID, directInput)
      if (conversationType === ConversationType.Copilot)
        return sendServiceCopilotTextMessage(conversationID, directInput)
      return sendDirectTextMessage(conversationID, directInput)
    }
    case ConversationType.Group:
      return sendGroupTextMessage(conversationID, {
        ...messageInput,
        replyToMessageId: replyToMessageID,
        mentionSubjectIds: mentions.flatMap((mention) =>
          mention.chatSubjectID ? [mention.chatSubjectID] : [],
        ),
        mentionAll,
      })
    default:
      throw new Error("不支持的会话类型")
  }
}
