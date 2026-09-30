/** 企业 AI 员工调用。 */
import {
  CreateAgent,
  DeactivateAgent,
  GetAgent,
  ListAgentModelOptions,
  ListAgentMCPServerOptions,
  ListAgents,
  ReactivateAgent,
  UpdateAgent,
  UpdateAgentExecution,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import {
  AgentExecutionMode,
  type Agent,
  type UpdateAgentExecutionInput,
  type AgentList,
  type AgentListInput,
  type AgentListItem,
  type CreateAgentInput,
  type UpdateAgentInput,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

type AgentListQuery = Partial<AgentListInput>

type ManagedAgentExecutionData = Omit<
  NonNullArrays<Agent>["execution"],
  "mode" | "managed"
> & {
  mode: AgentExecutionMode.AgentExecutionModeManaged
  managed: NonNullable<NonNullArrays<Agent>["execution"]["managed"]>
}

type ManagedAgentExecutionSummaryData = Omit<
  NonNullArrays<AgentListItem>["execution"],
  "mode" | "managed"
> & {
  mode: AgentExecutionMode.AgentExecutionModeManaged
  managed: NonNullable<NonNullArrays<AgentListItem>["execution"]["managed"]>
}

export type AgentData = Omit<NonNullArrays<Agent>, "execution"> & {
  execution: ManagedAgentExecutionData
}

export type AgentListItemData = Omit<NonNullArrays<AgentListItem>, "execution"> & {
  execution: ManagedAgentExecutionSummaryData
}

type AgentListData = Omit<NonNullArrays<AgentList>, "agents"> & {
  agents: AgentListItemData[]
}

const listAgentMCPServerOptionsBound = bind(ListAgentMCPServerOptions)
const listAgentsBound = bind(ListAgents)
const listAgentModelOptionsBound = bind(ListAgentModelOptions)
const getAgentBound = bind(GetAgent)
const createAgentBound = bind(CreateAgent)
const updateAgentBound = bind(UpdateAgent)
const updateAgentExecutionBound = bind(UpdateAgentExecution)
const deactivateAgentBound = bind(DeactivateAgent)
const reactivateAgentBound = bind(ReactivateAgent)

/** 创建企业 AI 员工。 */
export function createAgent(input: CreateAgentInput) {
  return createAgentBound(input).then(asManagedAgent)
}

/** 读取企业 AI 员工可使用的对话模型。 */
export function listAgentModelOptions() {
  return listAgentModelOptionsBound().then((output) => output.models)
}

/** 读取企业 MCP 服务的配置选项。 */
export function listAgentMCPServerOptions() {
  return listAgentMCPServerOptionsBound().then((output) => output.mcpServers)
}

/** 读取企业 AI 员工详情。 */
export function getAgent(agentId: string, signal?: AbortSignal) {
  return getAgentBound(agentId, signal).then(asManagedAgent)
}

/** 修改企业 AI 员工。 */
export function updateAgent(agentId: string, input: UpdateAgentInput) {
  return updateAgentBound(agentId, input).then(asManagedAgent)
}

/** 修改企业 AI 员工的执行配置。 */
export function updateAgentExecution(
  agentId: string,
  input: UpdateAgentExecutionInput,
) {
  return updateAgentExecutionBound(agentId, input).then(asManagedAgent)
}

/** 禁用企业 AI 员工账号。 */
export function deactivateAgent(agentId: string) {
  return deactivateAgentBound(agentId).then(asManagedAgent)
}

/** 将企业 AI 员工恢复为正常状态。 */
export function reactivateAgent(agentId: string) {
  return reactivateAgentBound(agentId).then(asManagedAgent)
}

/** 读取企业 AI 员工目录。 */
export function listAgents(
  query: AgentListQuery,
  signal?: AbortSignal,
): Promise<AgentListData> {
  return listAgentsBound(
    {
      query: query.query ?? "",
      status: query.status ?? null,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
    },
    signal,
  ).then((output) => {
    for (const agent of output.agents) assertManagedExecution(agent.execution)
    return output as AgentListData
  })
}

/** 校验 AI 员工使用平台托管执行配置。 */
function assertManagedExecution(execution: {
  mode: AgentExecutionMode
  managed?: unknown
}) {
  if (
    execution.mode !== AgentExecutionMode.AgentExecutionModeManaged ||
    !execution.managed
  ) {
    throw new Error(`Unsupported agent execution mode: ${execution.mode}`)
  }
}

/** 断言 AI 员工使用平台托管执行配置。 */
function asManagedAgent(agent: NonNullArrays<Agent>): AgentData {
  assertManagedExecution(agent.execution)
  return agent as AgentData
}
