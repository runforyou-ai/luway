/** 转人工原因的共享界面文案映射。 */
import { AgentHandoffReason } from "@/api"

/** 返回转人工原因对应的 inbox 词条键。 */
export function handoffReasonKey(reason: AgentHandoffReason | null | undefined) {
  switch (reason) {
    case AgentHandoffReason.AgentHandoffReasonKnowledgeGap:
      return "handoffReasonKnowledgeGap" as const
    case AgentHandoffReason.AgentHandoffReasonCustomerRequested:
      return "handoffReasonCustomerRequested" as const
    case AgentHandoffReason.AgentHandoffReasonNeedsHumanJudgment:
      return "handoffReasonNeedsHumanJudgment" as const
    case AgentHandoffReason.AgentHandoffReasonComplaint:
      return "handoffReasonComplaint" as const
    case AgentHandoffReason.AgentHandoffReasonInsufficientEvidence:
      return "handoffReasonInsufficientEvidence" as const
    case AgentHandoffReason.AgentHandoffReasonBudgetExhausted:
      return "handoffReasonBudgetExhausted" as const
    case AgentHandoffReason.AgentHandoffReasonInvalidOutput:
      return "handoffReasonInvalidOutput" as const
    case AgentHandoffReason.AgentHandoffReasonTimeout:
      return "handoffReasonTimeout" as const
    case AgentHandoffReason.AgentHandoffReasonAgentUnavailable:
      return "agentUnavailable" as const
    default:
      return "handoffReasonRuntimeFailed" as const
  }
}
