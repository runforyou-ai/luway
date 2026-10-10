/** 按会话对端账号状态生成发送框禁用提示。 */
import { useTranslation } from "react-i18next"

import { PersonalAgentPresence, UserStatus } from "@/api"

/** 对端 AI 员工（没有进行中的服务周期时）、个人 AI 员工或成员已禁用，或个人 AI 员工已暂停、绑定电脑已撤销时返回发送框提示，其余返回 null。 */
export function useAccountDisabledReason(
  conversation: {
    agent: {
      agentStatus: UserStatus
      personalPresence: PersonalAgentPresence | null
      serviceOpen: boolean
    } | null
    direct: { peerStatus: UserStatus } | null
  } | null,
) {
  const { t } = useTranslation("inbox")
  // AI 员工停用后，进行中的服务周期仍由负责的同事继续处理。
  if (conversation?.agent?.agentStatus === UserStatus.Inactive && !conversation.agent.serviceOpen) {
    return t(
      conversation.agent.personalPresence !== null
        ? "personalAgentDisabledUnavailable"
        : "agentDisabledUnavailable",
    )
  }
  if (conversation?.agent?.personalPresence === PersonalAgentPresence.Paused) {
    return t("personalAgentPausedUnavailable")
  }
  if (conversation?.agent?.personalPresence === PersonalAgentPresence.Unbound) {
    return t("personalAgentUnboundUnavailable")
  }
  if (conversation?.direct?.peerStatus === UserStatus.Inactive) {
    return t("directPeerDisabledUnavailable")
  }
  return null
}
