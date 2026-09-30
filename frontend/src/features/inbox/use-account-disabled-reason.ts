/** 按会话对端账号状态生成发送框禁用提示。 */
import { useTranslation } from "react-i18next"

import { AssistantPresence, OrganizationIdentityType, UserStatus } from "@/api"

/** 对端 AI 员工（没有进行中的服务周期时）、助理或成员已禁用，或助理已暂停、绑定电脑已撤销时返回发送框提示，其余返回 null。 */
export function useAccountDisabledReason(
  conversation: {
    agent: {
      agentStatus: UserStatus
      agentType: OrganizationIdentityType
      assistantPresence: AssistantPresence | null
      serviceOpen: boolean
    } | null
    direct: { peerStatus: UserStatus } | null
  } | null,
) {
  const { t } = useTranslation("inbox")
  // AI 员工停用后，进行中的服务周期仍由负责的同事继续处理。
  if (conversation?.agent?.agentStatus === UserStatus.UserStatusInactive && !conversation.agent.serviceOpen) {
    return t(
      conversation.agent.agentType === OrganizationIdentityType.OrganizationIdentityTypeAssistant
        ? "assistantDisabledUnavailable"
        : "agentDisabledUnavailable",
    )
  }
  if (conversation?.agent?.assistantPresence === AssistantPresence.AssistantPresencePaused) {
    return t("assistantPausedUnavailable")
  }
  if (conversation?.agent?.assistantPresence === AssistantPresence.AssistantPresenceUnbound) {
    return t("assistantUnboundUnavailable")
  }
  if (conversation?.direct?.peerStatus === UserStatus.UserStatusInactive) {
    return t("directPeerDisabledUnavailable")
  }
  return null
}
