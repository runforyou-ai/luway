/** 单聊与 AI 会话资料中的身份类型与 AI 运行状态字段。 */
import { useTranslation } from "react-i18next"

import {
  OrganizationIdentityType,
  isAgentInboxConversation,
  isDirectInboxConversation,
  type InboxConversationData,
  type MemberOption,
} from "@/api"
import { agentRunStatusLabel } from "@/features/inbox/agent-run-status"
import {
  SidePanelField,
  type ProfileField,
} from "@/features/inbox/side-panel-layout"
import { isAIIdentityType } from "@/lib/identity-type"

/** 展示对端身份类型，AI 会话有运行状态时一并展示；字段行默认使用侧栏样式。 */
export function InternalConversationDetails({
  conversation,
  directTarget = null,
  field: Field = SidePanelField,
}: {
  conversation: InboxConversationData | null
  directTarget?: MemberOption | null
  field?: ProfileField
}) {
  const { t } = useTranslation("inbox")
  const direct =
    conversation && isDirectInboxConversation(conversation)
      ? conversation.direct
      : null
  const agent =
    conversation && isAgentInboxConversation(conversation)
      ? conversation.agent
      : null
  const peerType = agent?.agentType ?? direct?.peerType ?? directTarget?.type
  const identityType =
    peerType === OrganizationIdentityType.OrganizationIdentityTypeAssistant
      ? t("contextIdentityAssistant")
      : isAIIdentityType(peerType)
        ? t("contextIdentityAgent")
        : t("contextIdentityMember")
  const agentStatus = agentRunStatusLabel(agent?.agentRunStatus ?? null, t)

  return (
    <>
      {direct || agent || directTarget ? (
        <Field label={t("contextIdentityType")}>{identityType}</Field>
      ) : null}
      {agent && agentStatus ? (
        <Field label={t("contextAgentStatus")}>{agentStatus}</Field>
      ) : null}
    </>
  )
}
