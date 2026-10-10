/** AI 员工回答质量评测调用。 */
import type { ServiceAudience } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

/** 评测用例可选的提问人服务对象。 */
export type AgentEvaluationAudienceId = typeof ServiceAudience.Customer | typeof ServiceAudience.Employee

/** 读取 AI 员工评测页的最近两次运行与全部用例。 */
export const getAgentEvaluation = ops.getAgentEvaluation

/** 读取评测用例与它在最近一次运行中的全部尝试。 */
export const getAgentEvaluationCase = ops.getAgentEvaluationCase

/** 新建手动评测用例。 */
export const createAgentEvaluationCase = ops.createAgentEvaluationCase

/** 修改评测用例。 */
export const updateAgentEvaluationCase = ops.updateAgentEvaluationCase

/** 把应转人工未转的问题会话以选定的客户消息为提问加入负责 AI 员工的评测。 */
export function addServiceIssueToEvaluation(serviceSessionId: string, questionMessageId: string) {
  return ops.addServiceIssueToEvaluation(serviceSessionId, { questionMessageId })
}

/** 删除评测用例。 */
export const deleteAgentEvaluationCase = ops.deleteAgentEvaluationCase

/** 用 AI 员工当前生效的配置对全部用例发起一次评测运行。 */
export const startAgentEvaluationRun = ops.startAgentEvaluationRun

/** 在最近一次运行中重新运行一条用例。 */
export const rerunAgentEvaluationCase = ops.rerunAgentEvaluationCase
