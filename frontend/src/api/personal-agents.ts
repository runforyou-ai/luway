/** 个人 AI 员工调用。 */
import {
  AgentExecutionMode,
  type PersonalAgent,
  type PersonalAgentDetail,
  type PersonalAgentList,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

/** 个人 AI 员工执行配置：平台托管执行带 managed。 */
type PersonalAgentExecution<T extends { managed?: unknown }> = Omit<T, "mode" | "managed"> & {
  mode: typeof AgentExecutionMode.Managed
  managed: NonNullable<T["managed"]>
}

export type PersonalAgentData = Omit<PersonalAgent, "execution"> & {
  execution: PersonalAgentExecution<PersonalAgent["execution"]>
}

export type PersonalAgentDetailData = Omit<PersonalAgentDetail, "personalAgent" | "execution"> & {
  personalAgent: PersonalAgentData
  execution: PersonalAgentExecution<PersonalAgentDetail["execution"]>
}

type PersonalAgentListData = Omit<PersonalAgentList, "personalAgents"> & {
  personalAgents: PersonalAgentData[]
}

/** 读取当前成员负责的个人 AI 员工。 */
export function listPersonalAgents() {
  return ops.listPersonalAgents().then(asPersonalAgentList)
}

/** 读取指定成员负责的个人 AI 员工。 */
export function listMemberPersonalAgents(userId: string) {
  return ops.listMemberPersonalAgents(userId).then(asPersonalAgentList)
}

/** 读取当前成员负责的个人 AI 员工详情与完整执行配置。 */
export function getPersonalAgent(agentId: string) {
  return ops.getPersonalAgent(agentId).then(
    (detail) =>
      ({
        personalAgent: asPersonalAgent(detail.personalAgent),
        execution: asPersonalAgentExecution(detail.execution),
      }) as PersonalAgentDetailData,
  )
}

/** 在本机创建个人 AI 员工。 */
export function createPersonalAgent(...args: Parameters<typeof ops.createPersonalAgent>) {
  return ops.createPersonalAgent(...args).then(asPersonalAgent)
}

/** 修改个人 AI 员工的资料与执行配置。 */
export function updatePersonalAgent(...args: Parameters<typeof ops.updatePersonalAgent>) {
  return ops.updatePersonalAgent(...args).then(asPersonalAgent)
}

/** 暂停个人 AI 员工。 */
export function pausePersonalAgent(agentId: string) {
  return ops.pausePersonalAgent(agentId).then(asPersonalAgent)
}

/** 恢复已暂停的个人 AI 员工。 */
export function resumePersonalAgent(agentId: string) {
  return ops.resumePersonalAgent(agentId).then(asPersonalAgent)
}

/** 把个人 AI 员工换到指定电脑。 */
export function movePersonalAgent(agentId: string, computerId: string) {
  return ops.movePersonalAgent(agentId, { computerId }).then(asPersonalAgent)
}

/** 禁用个人 AI 员工。 */
export function deactivatePersonalAgent(agentId: string) {
  return ops.deactivatePersonalAgent(agentId).then(asPersonalAgent)
}

/** 将个人 AI 员工恢复正常。 */
export function reactivatePersonalAgent(agentId: string) {
  return ops.reactivatePersonalAgent(agentId).then(asPersonalAgent)
}

/** 读取个人 AI 员工的记忆，按最近更新排列。 */
export const listAgentMemories = ops.listAgentMemories

/** 修改个人 AI 员工的一条记忆。 */
export const updateAgentMemory = ops.updateAgentMemory

/** 删除个人 AI 员工的一条记忆。 */
export const deleteAgentMemory = ops.deleteAgentMemory

/** 断言个人 AI 员工列表中的每一项均为有效在线状态与执行配置。 */
function asPersonalAgentList(list: PersonalAgentList): PersonalAgentListData {
  return { personalAgents: list.personalAgents.map(asPersonalAgent) }
}

/** 断言个人 AI 员工的执行配置与执行方式一致。 */
function asPersonalAgent(agent: PersonalAgent): PersonalAgentData {
  asPersonalAgentExecution(agent.execution)
  return agent as PersonalAgentData
}

/** 校验个人 AI 员工执行配置为带 managed 的平台托管执行。 */
function asPersonalAgentExecution<T extends { mode: AgentExecutionMode; managed?: unknown }>(execution: T) {
  if (execution.mode !== AgentExecutionMode.Managed || !execution.managed) {
    throw new Error(`Unsupported personal agent execution mode: ${execution.mode}`)
  }
  return execution
}
