/** 客服报表的问题类型判定。 */
import { ServiceIssueType, ServiceSessionSatisfaction, type ServiceIssue } from "@/api"

/** 返回问题会话在 candidates 中成立的问题类型，按 candidates 的顺序排列。 */
export function issueTypesOf(issue: ServiceIssue, candidates: readonly ServiceIssueType[]) {
  const matched: Partial<Record<ServiceIssueType, boolean>> = {
    [ServiceIssueType.Dissatisfied]:
      issue.satisfaction === ServiceSessionSatisfaction.Dissatisfied,
    [ServiceIssueType.AIIncorrect]: issue.aiIncorrect,
    [ServiceIssueType.AIMissedHandoff]: issue.aiMissedHandoff,
    [ServiceIssueType.AIPoorAttitude]: issue.aiPoorAttitude,
    [ServiceIssueType.HumanIncorrect]: issue.humanIncorrect,
    [ServiceIssueType.HumanPoorAttitude]: issue.humanPoorAttitude,
  }
  return candidates.filter((value) => matched[value])
}
