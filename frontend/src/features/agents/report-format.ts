/** 客服报表的问题类型判定。 */
import {
  ServiceIssueType,
  ServiceSessionSatisfaction,
  type ServiceIssueData,
  type ServiceIssueTypeId,
} from "@/api"

/** 返回问题会话在 candidates 中成立的问题类型，按 candidates 的顺序排列。 */
export function issueTypesOf(issue: ServiceIssueData, candidates: readonly ServiceIssueTypeId[]) {
  const matched: Partial<Record<ServiceIssueTypeId, boolean>> = {
    [ServiceIssueType.ServiceIssueTypeDissatisfied]:
      issue.satisfaction === ServiceSessionSatisfaction.ServiceSessionSatisfactionDissatisfied,
    [ServiceIssueType.ServiceIssueTypeAIIncorrect]: issue.aiIncorrect,
    [ServiceIssueType.ServiceIssueTypeAIMissedHandoff]: issue.aiMissedHandoff,
    [ServiceIssueType.ServiceIssueTypeAIPoorAttitude]: issue.aiPoorAttitude,
    [ServiceIssueType.ServiceIssueTypeHumanIncorrect]: issue.humanIncorrect,
    [ServiceIssueType.ServiceIssueTypeHumanPoorAttitude]: issue.humanPoorAttitude,
  }
  return candidates.filter((value) => matched[value])
}
