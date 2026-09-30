/** 会话消息读取、已读与提及水位、单聊与 AI 聊天发送调用。 */
import {
  FindDirectConversation,
  GetAgentRunProcess,
  GetAttachmentDownload,
  GetConversationMessageContext,
  GetConversationNavigationState,
  ListConversationMessages,
  ListPendingConversationMentions,
  MarkConversationMentionReviewed,
  MarkConversationRead,
  ReadConversationMessageWindow,
  ResolveCustomerMessageDelivery,
  SendAgentTextMessage,
  SendAttachmentMessage,
  SendDirectTextMessage,
  SendFirstAgentTextMessage,
  SendFirstDirectTextMessage,
  StopAgentReply,
  StopGroupAgentReply,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import type {
  ConversationAgentProcess,
  ConversationMessage,
  ConversationMessageList,
  ConversationMessageListInput,
  FirstAgentTextMessageInput,
  FirstDirectTextMessageInput,
  MarkConversationReadInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import { enqueueConversationUnreadChange } from "@/api/conversation-read-queue"
import type { AgentInboxConversationData, DirectInboxConversationData } from "@/api/inbox"
import type { NonNullArrays } from "@/api/normalize"

export type ConversationMessageListData = NonNullArrays<ConversationMessageList>

export type ConversationAgentProcessData = NonNullArrays<ConversationAgentProcess>

export type ConversationMessageData = NonNullArrays<ConversationMessage>

const listConversationMessagesBound = bind(ListConversationMessages)

/** 分页读取成员可见的会话消息，未给出游标时读取最新一页。 */
export function listConversationMessages(
  conversationID: string,
  input: ConversationMessageListInput = { before: "", after: "" },
  signal?: AbortSignal,
) {
  return listConversationMessagesBound(conversationID, input, signal)
}

/** 重读已加载首尾游标之间的完整消息范围。 */
export const readConversationMessageWindow = bind(ReadConversationMessageWindow)

/** 读取目标消息周围的连续上下文。 */
export const getConversationMessageContext = bind(GetConversationMessageContext)

/** 读取群聊待查看数量及最新可见消息。 */
export const getConversationNavigationState = bind(GetConversationNavigationState)

/** 获取本轮固定的提及目标列表。 */
export const listPendingConversationMentions = bind(ListPendingConversationMentions)

const markConversationMentionReviewedBound = bind(MarkConversationMentionReviewed)

/** 确认一条实际查看的提及目标。 */
export function markConversationMentionReviewed(conversationID: string, messageID: string) {
  return markConversationMentionReviewedBound(conversationID, { messageId: messageID })
}

const markConversationReadBound = bind(MarkConversationRead)

/** 单调推进当前用户的会话已读水位，同时清除手动未读标记时与未读标记写入排队执行。 */
export function markConversationRead(conversationID: string, input: MarkConversationReadInput) {
  if (input.clearUnreadMark) {
    return enqueueConversationUnreadChange(conversationID, () =>
      markConversationReadBound(conversationID, input),
    )
  }
  return markConversationReadBound(conversationID, input)
}

/** 人工确认或重试一条客户消息投递。 */
export const resolveCustomerMessageDelivery = bind(ResolveCustomerMessageDelivery)

/** 发送已上传的附件消息，首发时创建会话。 */
export const sendAttachmentMessage = bind(SendAttachmentMessage)

/** 获取当前可见附件的下载请求。 */
export const getAttachmentDownload = bind(GetAttachmentDownload)

/** 按运行编号读取展开时才需要的过程内容和模型用量。 */
export const getAgentRunProcess = bind(GetAgentRunProcess)

/** 停止指定 AI 聊天中的回复并读取实际运行状态。 */
export const stopAgentReply = bind(StopAgentReply)

/** 停止群聊中指定 AI 的回复并读取实际运行状态。 */
export const stopGroupAgentReply = bind(StopGroupAgentReply)

const sendFirstDirectTextMessageBound = bind(SendFirstDirectTextMessage)

/** 发送首条单聊消息并返回最终会话。 */
export async function sendFirstDirectTextMessage(input: FirstDirectTextMessageInput) {
  const result = await sendFirstDirectTextMessageBound(input)
  return {
    ...result,
    conversation: result.conversation as DirectInboxConversationData,
  }
}

const findDirectConversationBound = bind(FindDirectConversation)

/** 按目标身份查找当前成员的活跃单聊。 */
export async function findDirectConversation(targetIdentityID: string) {
  const result = await findDirectConversationBound(targetIdentityID)
  return result.conversation as DirectInboxConversationData | null
}

/** 发送企业成员内部单聊文本消息。 */
export const sendDirectTextMessage = bind(SendDirectTextMessage)

const sendFirstAgentTextMessageBound = bind(SendFirstAgentTextMessage)

/** 确认 AI 草稿对应的会话并保存首条消息。 */
export async function sendFirstAgentTextMessage(input: FirstAgentTextMessageInput) {
  const result = await sendFirstAgentTextMessageBound(input)
  return {
    ...result,
    conversation: result.conversation as AgentInboxConversationData,
  }
}

/** 向指定 AI 会话发送成员消息。 */
export const sendAgentTextMessage = bind(SendAgentTextMessage)
