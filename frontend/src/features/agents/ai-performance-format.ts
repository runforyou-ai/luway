/** AI 表现报表的待补知识处理状态与问题类型选项。 */
import {
  KnowledgeGapStatus,
  ServiceIssueType,
  type KnowledgeGapStatusId,
  type ServiceIssueTypeId,
} from "@/api"

/** 待补知识的处理状态筛选，第一个为默认值。 */
export const gapStatuses: KnowledgeGapStatusId[] = [
  KnowledgeGapStatus.KnowledgeGapStatusPending,
  KnowledgeGapStatus.KnowledgeGapStatusAccepted,
  KnowledgeGapStatus.KnowledgeGapStatusDismissed,
]

/** AI 表现问题会话的类型筛选，第一个为默认值。 */
export const aiIssueTypes: ServiceIssueTypeId[] = [
  ServiceIssueType.ServiceIssueTypeAll,
  ServiceIssueType.ServiceIssueTypeDissatisfied,
  ServiceIssueType.ServiceIssueTypeAIIncorrect,
  ServiceIssueType.ServiceIssueTypeAIMissedHandoff,
  ServiceIssueType.ServiceIssueTypeAIPoorAttitude,
]
