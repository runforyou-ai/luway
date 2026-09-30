/** 真人单聊与 AI 聊天草稿的首条消息发送和会话缓存刷新。 */
import {
  sendFirstAgentTextMessage,
  sendFirstDirectTextMessage,
  type DirectTextMessageInput,
  type InboxConversationData,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 首条消息或附件建出会话后刷新对端单聊与消息缓存；跳转由各端页面决定。 */
export function useFirstChatMessage() {
  const invalidate = useResourceInvalidator()

  /** 刷新新建会话的消息，真人单聊同时刷新按对端读取的单聊。 */
  function refreshStarted(
    conversation: InboxConversationData,
    directPeerIdentityID?: string,
  ) {
    if (directPeerIdentityID)
      void invalidate(resourceKeys.directConversation(directPeerIdentityID))
    void invalidate(resourceKeys.conversationMessages(conversation.id))
  }

  return {
    refreshStarted,
    /** 向成员发送首条单聊消息并建出会话。 */
    async sendDirect(targetIdentityID: string, input: DirectTextMessageInput) {
      const result = await sendFirstDirectTextMessage({
        targetIdentityId: targetIdentityID,
        ...input,
      })
      refreshStarted(result.conversation, targetIdentityID)
      return result
    },
    /** 向 AI 员工发送草稿会话的首条消息并建出会话。 */
    async sendAgent(
      conversationID: string,
      agentIdentityID: string,
      input: DirectTextMessageInput,
    ) {
      const result = await sendFirstAgentTextMessage({
        conversationId: conversationID,
        agentIdentityId: agentIdentityID,
        clientMessageId: input.clientMessageId,
        body: input.body,
      })
      refreshStarted(result.conversation)
      return result
    },
  }
}
