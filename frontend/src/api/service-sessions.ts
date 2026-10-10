/** 客户会话服务周期的领取、转交、关闭、对客发送、AI 写回复与 Copilot 调用。 */
import type { ServiceSessionTargetKind } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

/** 领取或接管客户会话最新处理周期。 */
export const claimServiceSession = ops.claimServiceSession

/** 客服处理周期的转交去向。 */
type ServiceSessionTransferTarget =
  | { kind: typeof ServiceSessionTargetKind.ServiceSessionTargetMember; identityId: string }
  | { kind: typeof ServiceSessionTargetKind.ServiceSessionTargetTeam; teamId: string }
  | { kind: typeof ServiceSessionTargetKind.ServiceSessionTargetPublicQueue }

/** 把当前负责的处理周期转给成员、团队队列或公共队列。 */
export function transferServiceSession(conversationId: string, target: ServiceSessionTransferTarget) {
  return ops.transferServiceSession(conversationId, {
    kind: target.kind,
    teamId: "teamId" in target ? target.teamId : "",
    identityId: "identityId" in target ? target.identityId : "",
  })
}

/** 关闭客户会话最新处理周期。 */
export const closeServiceSession = ops.closeServiceSession

/** 重新打开客户会话并分配给当前身份。 */
export const reopenServiceSession = ops.reopenServiceSession

/** 读取客户会话当前周期的交接摘要与同一客户已关闭周期的小结。 */
export const getServiceSummaries = ops.getServiceSummaries

/** 修改已关闭客服处理周期的小结、是否解决与咨询分类。 */
export const updateServiceSessionSummary = ops.updateServiceSessionSummary

/** 发送成员客户会话文本消息。 */
export const sendServiceTextMessage = ops.sendServiceTextMessage

/** 发送成员客户会话附件消息。 */
export const sendServiceAttachmentMessage = ops.sendServiceAttachmentMessage

/** 返回可用于 AI 写回复的 AI 员工。 */
export async function listServiceReplyAgents() {
  const output = await ops.listServiceReplyAgents()
  return output.agents
}

/** 使用 AI 员工为客户会话生成对客回复候选。 */
export const generateServiceReplySuggestions = ops.generateServiceReplySuggestions

/** 读取客户会话按最近活动倒序排列的 Copilot 线程。 */
export async function listServiceCopilotThreads(conversationID: string) {
  const output = await ops.listServiceCopilotThreads(conversationID)
  return output.threads
}

/** 以首条提问创建客户会话的 Copilot 线程。 */
export const sendFirstServiceCopilotMessage = ops.sendFirstServiceCopilotMessage

/** 向 Copilot 线程发送提问。 */
export const sendServiceCopilotTextMessage = ops.sendServiceCopilotTextMessage

/** 停止 Copilot 线程中的回复并读取实际运行状态。 */
export const stopServiceCopilotReply = ops.stopServiceCopilotReply
