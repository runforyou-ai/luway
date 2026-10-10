/** 转人工原因的共享界面文案映射。 */
import { AgentHandoffReason } from "@/api"

/** 返回转人工原因对应的 inbox 词条键。 */
export function handoffReasonKey(reason: AgentHandoffReason | null | undefined) {
  switch (reason) {
    case AgentHandoffReason.KnowledgeGap:
      return "handoffReasonKnowledgeGap" as const
    case AgentHandoffReason.CustomerRequested:
      return "handoffReasonCustomerRequested" as const
    case AgentHandoffReason.NeedsHumanJudgment:
      return "handoffReasonNeedsHumanJudgment" as const
    case AgentHandoffReason.Complaint:
      return "handoffReasonComplaint" as const
    case AgentHandoffReason.InsufficientEvidence:
      return "handoffReasonInsufficientEvidence" as const
    case AgentHandoffReason.BudgetExhausted:
      return "handoffReasonBudgetExhausted" as const
    case AgentHandoffReason.InvalidOutput:
      return "handoffReasonInvalidOutput" as const
    case AgentHandoffReason.Timeout:
      return "handoffReasonTimeout" as const
    case AgentHandoffReason.AgentUnavailable:
      return "agentUnavailable" as const
    default:
      return "handoffReasonRuntimeFailed" as const
  }
}
