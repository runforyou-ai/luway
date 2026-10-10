/** 企业 AI 员工调用。 */
import {
  AgentExecutionMode,
  type Agent,
  type UpdateAgentExecutionInput,
  type AgentList,
  type AgentListInput,
  type AgentListItem,
  type CreateAgentInput,
  type UpdateAgentInput,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

type AgentListQuery = Partial<AgentListInput>

type ManagedAgentExecutionData = Omit<Agent["execution"], "mode" | "managed"> & {
  mode: typeof AgentExecutionMode.Managed
  managed: NonNullable<Agent["execution"]["managed"]>
}

type AgentListExecutionSummary = AgentListItem["execution"]

/** 目录项执行配置摘要：平台托管执行带 managed。 */
type AgentExecutionSummaryData = Omit<AgentListExecutionSummary, "mode" | "managed"> & {
  mode: typeof AgentExecutionMode.Managed
  managed: NonNullable<AgentListExecutionSummary["managed"]>
}

export type AgentData = Omit<Agent, "execution"> & {
  execution: ManagedAgentExecutionData
}

export type AgentListItemData = Omit<AgentListItem, "execution"> & {
  execution: AgentExecutionSummaryData
}

type AgentListData = Omit<AgentList, "agents"> & {
  agents: AgentListItemData[]
}

/** 创建企业 AI 员工。 */
export function createAgent(input: CreateAgentInput) {
  return ops.createAgent(input).then(asManagedAgent)
}

/** 读取工作区业务系统的授权选项。 */
export function listAgentBusinessSystemOptions() {
  return ops.listAgentBusinessSystemOptions().then((output) => output.businessSystems)
}

/** 读取 AI 员工对成员公开的资料，供发起聊天时展示。 */
export const getAgentProfile = ops.getAgentProfile

/** 读取企业 AI 员工详情。 */
export function getAgent(agentId: string, signal?: AbortSignal) {
  return ops.getAgent(agentId, signal).then(asManagedAgent)
}

/** 修改企业 AI 员工。 */
export function updateAgent(agentId: string, input: UpdateAgentInput) {
  return ops.updateAgent(agentId, input).then(asManagedAgent)
}

/** 修改企业 AI 员工的执行配置。 */
export function updateAgentExecution(
  agentId: string,
  input: UpdateAgentExecutionInput,
) {
  return ops.updateAgentExecution(agentId, input).then(asManagedAgent)
}

/** 禁用企业 AI 员工账号。 */
export function deactivateAgent(agentId: string) {
  return ops.deactivateAgent(agentId).then(asManagedAgent)
}

/** 将企业 AI 员工恢复为正常状态。 */
export function reactivateAgent(agentId: string) {
  return ops.reactivateAgent(agentId).then(asManagedAgent)
}

/** 读取企业 AI 员工目录。 */
export function listAgents(
  query: AgentListQuery,
  signal?: AbortSignal,
): Promise<AgentListData> {
  return ops.listAgents(
    {
      query: query.query ?? "",
      status: query.status,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  ).then((output) => {
    for (const agent of output.agents) assertListItem(agent)
    return output as AgentListData
  })
}

/** 校验目录项：全部 AI 员工使用平台托管执行。 */
function assertListItem(agent: AgentListItem) {
  assertManagedExecution(agent.execution)
}

/** 校验 AI 员工使用平台托管执行配置。 */
function assertManagedExecution(execution: {
  mode: AgentExecutionMode
  managed?: unknown
}) {
  if (
    execution.mode !== AgentExecutionMode.Managed ||
    !execution.managed
  ) {
    throw new Error(`Unsupported agent execution mode: ${execution.mode}`)
  }
}

/** 断言 AI 员工使用平台托管执行配置。 */
function asManagedAgent(agent: Agent): AgentData {
  assertManagedExecution(agent.execution)
  return agent as AgentData
}
