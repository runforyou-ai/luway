/** 运营报表调用。 */
import {
  GetAIPerformanceReport,
  GetServiceIssue,
  GetTeamPerformanceReport,
  ListAgentServiceSessions,
  ListAIPerformanceBreakdowns,
  ListAIPerformanceIssues,
  ListTeamPerformanceBreakdowns,
  ListTeamPerformanceIssues,
  ListTeamPerformanceMembers,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import type {
  AIPerformanceIssueListInput,
  AIPerformanceReport,
  ServiceIssue,
  ServiceIssueDetail,
  ServiceIssueList,
  ServiceIssueType,
  ServiceSessionSatisfaction,
  TeamPerformanceIssueListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { ServiceTranscriptMessageData } from "@/api/knowledge-gaps"
import type { NonNullArrays } from "@/api/normalize"

export type AIPerformanceReportData = NonNullArrays<AIPerformanceReport>

export type ServiceIssueTypeId = Exclude<ServiceIssueType, ServiceIssueType.$zero>

export type ServiceIssueData = Omit<ServiceIssue, "satisfaction"> & {
  satisfaction: Exclude<ServiceSessionSatisfaction, ServiceSessionSatisfaction.$zero> | null
}

export type ServiceIssueListData = Omit<NonNullArrays<ServiceIssueList>, "issues"> & {
  issues: ServiceIssueData[]
}

type ServiceIssueDetailData = Omit<NonNullArrays<ServiceIssueDetail>, "issue" | "messages"> & {
  issue: ServiceIssueData
  messages: ServiceTranscriptMessageData[]
}

const listAIPerformanceIssuesBound = bind(ListAIPerformanceIssues)
const listTeamPerformanceIssuesBound = bind(ListTeamPerformanceIssues)
const getServiceIssueBound = bind(GetServiceIssue)

/** 读取当前企业指定范围内的 AI 客服表现概览。 */
export const getAIPerformanceReport = bind(GetAIPerformanceReport)

/** 读取按渠道或咨询分类拆分的一页 AI 客服表现。 */
export const listAIPerformanceBreakdowns = bind(ListAIPerformanceBreakdowns)

/** 读取一页指定类型的 AI 表现问题会话。 */
export function listAIPerformanceIssues(input: AIPerformanceIssueListInput) {
  return listAIPerformanceIssuesBound(input) as Promise<ServiceIssueListData>
}

/** 读取当前企业指定范围内的真人客服表现概览。 */
export const getTeamPerformanceReport = bind(GetTeamPerformanceReport)

/** 读取按客服拆分的一页真人客服表现。 */
export const listTeamPerformanceMembers = bind(ListTeamPerformanceMembers)

/** 读取按渠道或咨询分类拆分的一页真人客服表现。 */
export const listTeamPerformanceBreakdowns = bind(ListTeamPerformanceBreakdowns)

/** 读取一页指定类型的真人接待问题会话。 */
export function listTeamPerformanceIssues(input: TeamPerformanceIssueListInput) {
  return listTeamPerformanceIssuesBound(input) as Promise<ServiceIssueListData>
}

/** 读取客服周期的质检结论与对客沟通。 */
export function getServiceIssue(serviceSessionId: string, signal?: AbortSignal) {
  return getServiceIssueBound(serviceSessionId, signal) as Promise<ServiceIssueDetailData>
}

/** 读取 AI 员工接待的一页服务周期。 */
export const listAgentServiceSessions = bind(ListAgentServiceSessions)
