/** AI 员工操作的确认、审批与核对调用。 */
import * as ops from "@/api/generated/operations"

/** 读取待当前成员确认、审批或核对的 AI 员工操作，按提交时间倒序排列。 */
export const listAgentToolDecisions = ops.listAgentToolDecisions

/** 确认、批准或拒绝 AI 员工提交的操作。 */
export const decideAgentToolCall = ops.decideAgentToolCall

/** 把结果待核对的 AI 员工操作标记为已核对。 */
export const reviewAgentToolCall = ops.reviewAgentToolCall
