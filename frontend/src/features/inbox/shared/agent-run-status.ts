/** Agent 单聊运行状态与个人 AI 员工在线状态文案。 */
import type { TFunction } from "i18next"

import { AgentRunStatus, PersonalAgentPresence } from "@/api"

/** 返回 Agent 单聊运行状态的用户文案。 */
export function agentRunStatusLabel(
  status: AgentRunStatus | null,
  t: TFunction<"inbox">,
) {
  switch (status) {
    case AgentRunStatus.Queued:
      return t("agentRunQueued")
    case AgentRunStatus.Running:
      return t("agentRunRunning")
    case AgentRunStatus.Waiting:
      return t("agentRunAwaitingResult")
    case AgentRunStatus.Failed:
      return t("agentRunFailed")
    default:
      return ""
  }
}

/** 返回个人 AI 员工不在线时的原因文案，离线时说明上线后回复，在线或未知状态返回 null。 */
export function personalAgentUnavailableLabel(
  presence: PersonalAgentPresence | null | undefined,
  t: TFunction<["inbox", "agents"]>,
) {
  switch (presence) {
    case PersonalAgentPresence.Offline:
      return t("inbox:personalAgentPresenceOffline")
    case PersonalAgentPresence.Paused:
      return t("agents:personal.presence.paused")
    case PersonalAgentPresence.Unbound:
      return t("agents:personal.presence.unbound")
    default:
      return null
  }
}
