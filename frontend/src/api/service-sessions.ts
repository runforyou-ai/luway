/** 客户会话服务周期的领取、转交、关闭、对客发送、AI 写回复与 Copilot 调用。 */
import {
  ClaimServiceSession,
  CloseServiceSession,
  GenerateServiceReplySuggestions,
  GetServiceSummaries,
  ListServiceCopilotThreads,
  ListServiceReplyAgents,
  ReopenServiceSession,
  SendFirstServiceCopilotMessage,
  SendServiceAttachmentMessage,
  SendServiceCopilotTextMessage,
  SendServiceTextMessage,
  StopServiceCopilotReply,
  TransferServiceSession,
  UpdateServiceSessionSummary,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { ServiceSessionTargetKind } from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"

/** 领取或接管客户会话最新处理周期。 */
export const claimServiceSession = bind(ClaimServiceSession)

/** 客服处理周期的转交去向。 */
type ServiceSessionTransferTarget =
  | { kind: typeof ServiceSessionTargetKind.ServiceSessionTargetMember; identityId: string }
  | { kind: typeof ServiceSessionTargetKind.ServiceSessionTargetTeam; teamId: string }
  | { kind: typeof ServiceSessionTargetKind.ServiceSessionTargetPublicQueue }

const transferServiceSessionBound = bind(TransferServiceSession)

/** 把当前负责的处理周期转给成员、团队队列或公共队列。 */
export function transferServiceSession(conversationId: string, target: ServiceSessionTransferTarget) {
  return transferServiceSessionBound(conversationId, {
    kind: target.kind,
    teamId: "teamId" in target ? target.teamId : "",
    identityId: "identityId" in target ? target.identityId : "",
  })
}

/** 关闭客户会话最新处理周期。 */
export const closeServiceSession = bind(CloseServiceSession)

/** 重新打开客户会话并分配给当前身份。 */
export const reopenServiceSession = bind(ReopenServiceSession)

/** 读取客户会话当前周期的交接摘要与同一客户已关闭周期的小结。 */
export const getServiceSummaries = bind(GetServiceSummaries)

/** 修改已关闭客服处理周期的小结、是否解决与咨询分类。 */
export const updateServiceSessionSummary = bind(UpdateServiceSessionSummary)

/** 发送成员客户会话文本消息。 */
export const sendServiceTextMessage = bind(SendServiceTextMessage)

/** 发送成员客户会话附件消息。 */
export const sendServiceAttachmentMessage = bind(SendServiceAttachmentMessage)

const listServiceReplyAgentsBound = bind(ListServiceReplyAgents)

/** 返回可用于 AI 写回复的 AI 员工。 */
export async function listServiceReplyAgents() {
  const output = await listServiceReplyAgentsBound()
  return output.agents
}

/** 使用 AI 员工为客户会话生成对客回复候选。 */
export const generateServiceReplySuggestions = bind(GenerateServiceReplySuggestions)

const listServiceCopilotThreadsBound = bind(ListServiceCopilotThreads)

/** 读取客户会话按最近活动倒序排列的 Copilot 线程。 */
export async function listServiceCopilotThreads(conversationID: string) {
  const output = await listServiceCopilotThreadsBound(conversationID)
  return output.threads
}

/** 以首条提问创建客户会话的 Copilot 线程。 */
export const sendFirstServiceCopilotMessage = bind(SendFirstServiceCopilotMessage)

/** 向 Copilot 线程发送提问。 */
export const sendServiceCopilotTextMessage = bind(SendServiceCopilotTextMessage)

/** 停止 Copilot 线程中的回复并读取实际运行状态。 */
export const stopServiceCopilotReply = bind(StopServiceCopilotReply)
