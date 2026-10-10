/** 待当前成员处理的 AI 员工操作。 */
import { listAgentToolDecisions } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 读取待当前成员确认、审批或核对的 AI 员工操作，清单变化时由同步协调器失效重读。 */
export function usePendingToolDecisions() {
  return useResource(resourceKeys.agentToolDecisions(), (signal) => listAgentToolDecisions(signal))
}

/** 返回待当前成员处理的 AI 员工操作条数。 */
export function usePendingToolDecisionCount() {
  return usePendingToolDecisions().data?.items.length ?? 0
}
