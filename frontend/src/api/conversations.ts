/** 会话消息读取、已读与提及水位、单聊与 AI 聊天发送调用。 */
import { enqueueConversationUnreadChange } from "@/api/conversation-read-queue"
import type {
  ConversationMessageListInput,
  FirstAgentTextMessageInput,
  FirstDirectTextMessageInput,
  MarkConversationReadInput,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import type { AgentInboxConversationData, DirectInboxConversationData } from "@/api/inbox"

/** 分页读取成员可见的会话消息，未给出游标时读取最新一页。 */
export function listConversationMessages(
  conversationID: string,
  input: ConversationMessageListInput = { before: "", after: "" },
  signal?: AbortSignal,
) {
  return ops.listConversationMessages(conversationID, input, signal)
}

/** 重读已加载首尾游标之间的完整消息范围。 */
export const readConversationMessageWindow = ops.readConversationMessageWindow

/** 读取目标消息周围的连续上下文。 */
export const getConversationMessageContext = ops.getConversationMessageContext

/** 读取群聊待查看数量及最新可见消息。 */
export const getConversationNavigationState = ops.getConversationNavigationState

/** 获取本轮固定的提及目标列表。 */
export const listPendingConversationMentions = ops.listPendingConversationMentions

/** 确认一条实际查看的提及目标。 */
export function markConversationMentionReviewed(conversationID: string, messageID: string) {
  return ops.markConversationMentionReviewed(conversationID, { messageId: messageID })
}

/** 单调推进当前用户的会话已读水位，同时清除手动未读标记时与未读标记写入排队执行。 */
export function markConversationRead(conversationID: string, input: MarkConversationReadInput) {
  if (input.clearUnreadMark) {
    return enqueueConversationUnreadChange(conversationID, () =>
      ops.markConversationRead(conversationID, input),
    )
  }
  return ops.markConversationRead(conversationID, input)
}

/** 人工确认或重试一条客户消息投递。 */
export const resolveChannelMessageDelivery = ops.resolveChannelMessageDelivery

/** 发送已上传的附件消息，首发时创建会话。 */
export const sendAttachmentMessage = ops.sendAttachmentMessage

/** 获取当前可见附件的下载请求。 */
export const getAttachmentDownload = ops.getAttachmentDownload

/** 按运行编号读取展开时才需要的过程内容和模型用量。 */
export const getAgentRunProcess = ops.getAgentRunProcess

/** 每次读取的过程更新条数上限，与服务端一致。 */
const toolCallUpdatePage = 500

/** 读取电脑执行的工具调用的当前状态与全部过程更新，超过一页时按序号继续读取。 */
export async function getAgentToolCallProcess(toolCallID: string, signal?: AbortSignal) {
  const first = await ops.getAgentToolCallProcess(toolCallID, { from: 1 }, signal)
  const updates = [...first.updates]
  for (let page = first.updates; page.length === toolCallUpdatePage; ) {
    const next = await ops.getAgentToolCallProcess(toolCallID, { from: updates[updates.length - 1].seq + 1 }, signal)
    page = next.updates
    updates.push(...page)
  }
  return { ...first, updates }
}

/** 停止委派给本机 Agent 的一轮并释放它所在的本机 Agent 会话。 */
export const stopLocalAgent = ops.stopLocalAgent

/** 停止指定 AI 聊天中的回复并读取实际运行状态。 */
export const stopAgentReply = ops.stopAgentReply

/** 停止群聊中指定 AI 的回复并读取实际运行状态。 */
export const stopGroupAgentReply = ops.stopGroupAgentReply

/** 发送首条单聊消息并返回最终会话。 */
export async function sendFirstDirectTextMessage(input: FirstDirectTextMessageInput) {
  const result = await ops.sendFirstDirectTextMessage(input)
  return {
    ...result,
    conversation: result.conversation as DirectInboxConversationData,
  }
}

/** 按目标身份查找当前成员的活跃单聊。 */
export async function findDirectConversation(targetIdentityID: string) {
  const result = await ops.findDirectConversation(targetIdentityID)
  return result.conversation as DirectInboxConversationData | null
}

/** 发送企业成员内部单聊文本消息。 */
export const sendDirectTextMessage = ops.sendDirectTextMessage

/** 确认 AI 草稿对应的会话并保存首条消息。 */
export async function sendFirstAgentTextMessage(input: FirstAgentTextMessageInput) {
  const result = await ops.sendFirstAgentTextMessage(input)
  return {
    ...result,
    conversation: result.conversation as AgentInboxConversationData,
  }
}

/** 向指定 AI 会话发送成员消息。 */
export const sendAgentTextMessage = ops.sendAgentTextMessage
