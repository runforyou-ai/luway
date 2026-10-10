/** AI 表现报表的待补知识处理状态与问题类型选项。 */
import { KnowledgeGapStatus, ServiceIssueType } from "@/api"

/** 待补知识的处理状态筛选，第一个为默认值。 */
export const gapStatuses: KnowledgeGapStatus[] = [
  KnowledgeGapStatus.Pending,
  KnowledgeGapStatus.Accepted,
  KnowledgeGapStatus.Dismissed,
]

/** AI 表现问题会话的类型筛选，第一个为默认值。 */
export const aiIssueTypes: ServiceIssueType[] = [
  ServiceIssueType.All,
  ServiceIssueType.Dissatisfied,
  ServiceIssueType.AIIncorrect,
  ServiceIssueType.AIMissedHandoff,
  ServiceIssueType.AIPoorAttitude,
]
