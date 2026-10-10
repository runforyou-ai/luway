/** 运营报表调用。 */
import * as ops from "@/api/generated/operations"

/** 读取当前企业指定范围内的 AI 客服表现概览。 */
export const getAIPerformanceReport = ops.getAIPerformanceReport

/** 读取按渠道或咨询分类拆分的一页 AI 客服表现。 */
export const listAIPerformanceBreakdowns = ops.listAIPerformanceBreakdowns

/** 读取一页指定类型的 AI 表现问题会话。 */
export const listAIPerformanceIssues = ops.listAIPerformanceIssues

/** 读取当前企业指定范围内的真人客服表现概览。 */
export const getTeamPerformanceReport = ops.getTeamPerformanceReport

/** 读取按客服拆分的一页真人客服表现。 */
export const listTeamPerformanceMembers = ops.listTeamPerformanceMembers

/** 读取按渠道或咨询分类拆分的一页真人客服表现。 */
export const listTeamPerformanceBreakdowns = ops.listTeamPerformanceBreakdowns

/** 读取一页指定类型的真人接待问题会话。 */
export const listTeamPerformanceIssues = ops.listTeamPerformanceIssues

/** 读取客服周期的质检结论与对客沟通。 */
export const getServiceIssue = ops.getServiceIssue

/** 读取 AI 员工接待的一页服务周期。 */
export const listAgentServiceSessions = ops.listAgentServiceSessions
