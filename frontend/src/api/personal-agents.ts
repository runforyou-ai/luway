/** 个人 AI 员工调用。 */
import {
  CreatePersonalAgent,
  DeactivatePersonalAgent,
  DeleteAgentMemory,
  GetPersonalAgent,
  ListAgentMemories,
  ListMemberPersonalAgents,
  ListPersonalAgents,
  MovePersonalAgent,
  PausePersonalAgent,
  ReactivatePersonalAgent,
  ResumePersonalAgent,
  UpdateAgentMemory,
  UpdatePersonalAgent,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AgentExecutionMode,
  LocalAgentKind,
  PersonalAgentPresence,
  type PersonalAgent,
  type PersonalAgentDetail,
  type PersonalAgentList,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type PersonalAgentPresenceId = Exclude<PersonalAgentPresence, PersonalAgentPresence.$zero>

export type LocalAgentKindId = Exclude<LocalAgentKind, LocalAgentKind.$zero>

/** 个人 AI 员工执行配置按执行方式区分：平台托管执行带 managed，本机 Agent 执行带 localAgent。 */
type PersonalAgentExecution<T extends { managed?: unknown; localAgent?: unknown }> =
  | (Omit<T, "mode" | "managed" | "localAgent"> & {
      mode: AgentExecutionMode.AgentExecutionModeManaged
      managed: NonNullable<T["managed"]>
      localAgent?: null
    })
  | (Omit<T, "mode" | "managed" | "localAgent"> & {
      mode: AgentExecutionMode.AgentExecutionModeLocalAgent
      managed?: null
      localAgent: Omit<NonNullable<T["localAgent"]>, "kind"> & { kind: LocalAgentKindId }
    })

export type PersonalAgentData = Omit<NonNullArrays<PersonalAgent>, "presence" | "execution" | "device"> & {
  presence: PersonalAgentPresenceId
  device: Omit<NonNullArrays<PersonalAgent>["device"], "localAgents"> & { localAgents: LocalAgentKindId[] }
  execution: PersonalAgentExecution<NonNullArrays<PersonalAgent>["execution"]>
}

export type PersonalAgentDetailData = Omit<NonNullArrays<PersonalAgentDetail>, "personalAgent" | "execution"> & {
  personalAgent: PersonalAgentData
  execution: PersonalAgentExecution<NonNullArrays<PersonalAgentDetail>["execution"]>
}

type PersonalAgentListData = Omit<NonNullArrays<PersonalAgentList>, "personalAgents"> & {
  personalAgents: PersonalAgentData[]
}

const listPersonalAgentsBound = bind(ListPersonalAgents)
const listMemberPersonalAgentsBound = bind(ListMemberPersonalAgents)
const getPersonalAgentBound = bind(GetPersonalAgent)
const createPersonalAgentBound = bind(CreatePersonalAgent)
const updatePersonalAgentBound = bind(UpdatePersonalAgent)
const pausePersonalAgentBound = bind(PausePersonalAgent)
const resumePersonalAgentBound = bind(ResumePersonalAgent)
const movePersonalAgentBound = bind(MovePersonalAgent)
const deactivatePersonalAgentBound = bind(DeactivatePersonalAgent)
const reactivatePersonalAgentBound = bind(ReactivatePersonalAgent)

/** 读取当前成员负责的个人 AI 员工。 */
export function listPersonalAgents() {
  return listPersonalAgentsBound().then(asPersonalAgentList)
}

/** 读取指定成员负责的个人 AI 员工。 */
export function listMemberPersonalAgents(userId: string) {
  return listMemberPersonalAgentsBound(userId).then(asPersonalAgentList)
}

/** 读取当前成员负责的个人 AI 员工详情与完整执行配置。 */
export function getPersonalAgent(agentId: string) {
  return getPersonalAgentBound(agentId).then(
    (detail) =>
      ({
        personalAgent: asPersonalAgent(detail.personalAgent),
        execution: asPersonalAgentExecution(detail.execution),
      }) as PersonalAgentDetailData,
  )
}

/** 在本机创建个人 AI 员工。 */
export function createPersonalAgent(...args: Parameters<typeof createPersonalAgentBound>) {
  return createPersonalAgentBound(...args).then(asPersonalAgent)
}

/** 修改个人 AI 员工的资料与执行配置。 */
export function updatePersonalAgent(...args: Parameters<typeof updatePersonalAgentBound>) {
  return updatePersonalAgentBound(...args).then(asPersonalAgent)
}

/** 暂停个人 AI 员工。 */
export function pausePersonalAgent(agentId: string) {
  return pausePersonalAgentBound(agentId).then(asPersonalAgent)
}

/** 恢复已暂停的个人 AI 员工。 */
export function resumePersonalAgent(agentId: string) {
  return resumePersonalAgentBound(agentId).then(asPersonalAgent)
}

/** 把个人 AI 员工换到指定电脑。 */
export function movePersonalAgent(agentId: string, deviceId: string) {
  return movePersonalAgentBound(agentId, { deviceId }).then(asPersonalAgent)
}

/** 禁用个人 AI 员工。 */
export function deactivatePersonalAgent(agentId: string) {
  return deactivatePersonalAgentBound(agentId).then(asPersonalAgent)
}

/** 将个人 AI 员工恢复正常。 */
export function reactivatePersonalAgent(agentId: string) {
  return reactivatePersonalAgentBound(agentId).then(asPersonalAgent)
}

/** 读取个人 AI 员工的记忆，按最近更新排列。 */
export const listAgentMemories = bind(ListAgentMemories)

/** 修改个人 AI 员工的一条记忆。 */
export const updateAgentMemory = bind(UpdateAgentMemory)

/** 删除个人 AI 员工的一条记忆。 */
export const deleteAgentMemory = bind(DeleteAgentMemory)

/** 断言个人 AI 员工列表中的每一项均为有效在线状态与执行配置。 */
function asPersonalAgentList(list: NonNullArrays<PersonalAgentList>): PersonalAgentListData {
  return { personalAgents: list.personalAgents.map(asPersonalAgent) }
}

/** 断言个人 AI 员工的在线状态有效且执行配置与执行方式一致。 */
function asPersonalAgent(agent: NonNullArrays<PersonalAgent>): PersonalAgentData {
  if (agent.presence === PersonalAgentPresence.$zero) {
    throw new Error("Personal agent presence is missing")
  }
  asPersonalAgentExecution(agent.execution)
  return agent as PersonalAgentData
}

/** 校验个人 AI 员工执行配置：平台托管执行带 managed，本机 Agent 执行带有效的 localAgent。 */
function asPersonalAgentExecution<
  T extends { mode: AgentExecutionMode; managed?: unknown; localAgent?: { kind: LocalAgentKind } | null },
>(execution: T) {
  const managed = execution.mode === AgentExecutionMode.AgentExecutionModeManaged && execution.managed
  const localAgent =
    execution.mode === AgentExecutionMode.AgentExecutionModeLocalAgent &&
    execution.localAgent &&
    execution.localAgent.kind !== LocalAgentKind.$zero
  if (!managed && !localAgent) {
    throw new Error(`Unsupported personal agent execution mode: ${execution.mode}`)
  }
  return execution
}
