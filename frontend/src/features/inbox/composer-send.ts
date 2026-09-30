/** 按会话类型调用对应的成员文本消息发送接口。 */
import {
  ConversationType,
  MessageVisibility,
  sendAgentTextMessage,
  sendServiceCopilotTextMessage,
  sendServiceTextMessage,
  sendDirectTextMessage,
  sendGroupTextMessage,
  type ConversationMessageData,
  type CustomerReplyTranslation,
  type DirectTextMessageInput,
} from "@/api"
import type { MentionTarget } from "@/lib/outgoing-message-store"

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
  ) => Promise<ConversationMessageData>
}): Promise<ConversationMessageData> {
  const messageInput = { clientMessageId: clientMessageID, body }
  if (service)
    return sendServiceTextMessage(conversationID, {
      ...messageInput,
      replyToMessageId: replyToMessageID,
      visibility,
      mentionIdentityIds: visibility === MessageVisibility.MessageVisibilityInternal
        ? mentions.map((mention) => mention.identityID)
        : [],
      translate: translate && visibility === MessageVisibility.MessageVisibilityShared,
      translation: visibility === MessageVisibility.MessageVisibilityShared ? translation : null,
    })
  switch (conversationType) {
    case ConversationType.ConversationTypeAgent:
    case ConversationType.ConversationTypeCopilot:
    case ConversationType.ConversationTypeDirect: {
      const directInput = {
        ...messageInput,
        replyToMessageId: replyToMessageID,
      }
      // 草稿首发走调用方入口，已有会话按类型发送。
      if (sendIndividualMessage) return sendIndividualMessage(directInput)
      if (conversationType === ConversationType.ConversationTypeAgent)
        return sendAgentTextMessage(conversationID, directInput)
      if (conversationType === ConversationType.ConversationTypeCopilot)
        return sendServiceCopilotTextMessage(conversationID, directInput)
      return sendDirectTextMessage(conversationID, directInput)
    }
    case ConversationType.ConversationTypeGroup:
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
