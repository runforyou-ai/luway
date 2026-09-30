/** 助理调用。 */
import {
  CreateAssistant,
  DeactivateAssistant,
  DeleteAssistantMemory,
  GetAssistant,
  ListAssistantMemories,
  ListAssistants,
  ListMemberAssistants,
  MoveAssistant,
  PauseAssistant,
  ReactivateAssistant,
  ResumeAssistant,
  UpdateAssistant,
  UpdateAssistantMemory,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  AgentExecutionMode,
  AssistantPresence,
  LocalAgentKind,
  type Assistant,
  type AssistantDetail,
  type AssistantList,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

type AssistantPresenceId = Exclude<AssistantPresence, AssistantPresence.$zero>

export type LocalAgentKindId = Exclude<LocalAgentKind, LocalAgentKind.$zero>

/** 助理执行配置按执行方式区分：平台托管执行带 managed，本机 Agent 执行带 localAgent。 */
type AssistantExecution<T extends { managed?: unknown; localAgent?: unknown }> =
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

export type AssistantData = Omit<NonNullArrays<Assistant>, "presence" | "execution" | "device"> & {
  presence: AssistantPresenceId
  device: Omit<NonNullArrays<Assistant>["device"], "localAgents"> & { localAgents: LocalAgentKindId[] }
  execution: AssistantExecution<NonNullArrays<Assistant>["execution"]>
}

export type AssistantDetailData = Omit<NonNullArrays<AssistantDetail>, "assistant" | "execution"> & {
  assistant: AssistantData
  execution: AssistantExecution<NonNullArrays<AssistantDetail>["execution"]>
}

type AssistantListData = Omit<NonNullArrays<AssistantList>, "assistants"> & { assistants: AssistantData[] }


const listAssistantsBound = bind(ListAssistants)
const listMemberAssistantsBound = bind(ListMemberAssistants)
const getAssistantBound = bind(GetAssistant)
const createAssistantBound = bind(CreateAssistant)
const updateAssistantBound = bind(UpdateAssistant)
const pauseAssistantBound = bind(PauseAssistant)
const resumeAssistantBound = bind(ResumeAssistant)
const moveAssistantBound = bind(MoveAssistant)
const deactivateAssistantBound = bind(DeactivateAssistant)
const reactivateAssistantBound = bind(ReactivateAssistant)

/** 读取当前成员名下的助理。 */
export function listAssistants() {
  return listAssistantsBound().then(asAssistantList)
}

/** 读取指定成员名下的助理。 */
export function listMemberAssistants(userId: string) {
  return listMemberAssistantsBound(userId).then(asAssistantList)
}

/** 读取当前成员名下的助理详情与完整执行配置。 */
export function getAssistant(assistantId: string) {
  return getAssistantBound(assistantId).then(
    (detail) => ({ assistant: asAssistant(detail.assistant), execution: asAssistantExecution(detail.execution) }) as AssistantDetailData,
  )
}

/** 在本机创建助理。 */
export function createAssistant(...args: Parameters<typeof createAssistantBound>) {
  return createAssistantBound(...args).then(asAssistant)
}

/** 修改助理的资料与执行配置。 */
export function updateAssistant(...args: Parameters<typeof updateAssistantBound>) {
  return updateAssistantBound(...args).then(asAssistant)
}

/** 暂停助理。 */
export function pauseAssistant(assistantId: string) {
  return pauseAssistantBound(assistantId).then(asAssistant)
}

/** 恢复已暂停的助理。 */
export function resumeAssistant(assistantId: string) {
  return resumeAssistantBound(assistantId).then(asAssistant)
}

/** 把助理换到指定电脑。 */
export function moveAssistant(assistantId: string, deviceId: string) {
  return moveAssistantBound(assistantId, { deviceId }).then(asAssistant)
}

/** 禁用助理。 */
export function deactivateAssistant(assistantId: string) {
  return deactivateAssistantBound(assistantId).then(asAssistant)
}

/** 将助理恢复正常。 */
export function reactivateAssistant(assistantId: string) {
  return reactivateAssistantBound(assistantId).then(asAssistant)
}

/** 读取助理的记忆，按最近更新排列。 */
export const listAssistantMemories = bind(ListAssistantMemories)

/** 修改助理的一条记忆。 */
export const updateAssistantMemory = bind(UpdateAssistantMemory)

/** 删除助理的一条记忆。 */
export const deleteAssistantMemory = bind(DeleteAssistantMemory)

/** 断言助理列表中的每一项均为有效在线状态与执行配置。 */
function asAssistantList(list: NonNullArrays<AssistantList>): AssistantListData {
  return { assistants: list.assistants.map(asAssistant) }
}

/** 断言助理的在线状态有效且执行配置与执行方式一致。 */
function asAssistant(assistant: NonNullArrays<Assistant>): AssistantData {
  if (assistant.presence === AssistantPresence.$zero) {
    throw new Error("Assistant presence is missing")
  }
  asAssistantExecution(assistant.execution)
  return assistant as AssistantData
}

/** 校验助理执行配置：平台托管执行带 managed，本机 Agent 执行带有效的 localAgent。 */
function asAssistantExecution<T extends { mode: AgentExecutionMode; managed?: unknown; localAgent?: { kind: LocalAgentKind } | null }>(execution: T) {
  const managed = execution.mode === AgentExecutionMode.AgentExecutionModeManaged && execution.managed
  const localAgent = execution.mode === AgentExecutionMode.AgentExecutionModeLocalAgent && execution.localAgent && execution.localAgent.kind !== LocalAgentKind.$zero
  if (!managed && !localAgent) {
    throw new Error(`Unsupported assistant execution mode: ${execution.mode}`)
  }
  return execution
}
