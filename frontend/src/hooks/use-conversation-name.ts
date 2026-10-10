/** 会话显示名解析。 */
import { useCallback } from "react"
import { useTranslation } from "react-i18next"

import {
  isAgentInboxConversation,
  isServiceInboxConversation,
  isDirectInboxConversation,
  isGroupInboxConversation,
  type GroupInboxConversationData,
  type InboxConversation,
} from "@/api"
import { useContactName } from "@/hooks/use-contact-name"

/** 群聊显示名：有名称时使用名称，未命名时按除自己外的成员名称拼接。 */
export function useGroupDisplayName() {
  const { t } = useTranslation("inbox")
  return useCallback(
    (
      group: Pick<GroupInboxConversationData["group"], "title" | "memberPreviewNames">,
      memberCount: number,
    ) => {
      const title = group.title.trim()
      if (title) return title
      const names = group.memberPreviewNames.join(t("groupNameSeparator"))
      if (!names) return t("groupUntitled")
      const more = memberCount - 1 - group.memberPreviewNames.length
      return more > 0
        ? t("groupMemberNamesMore", { names, count: memberCount, more })
        : names
    },
    [t],
  )
}

/** 会话在列表和主区中的显示名。 */
export function useConversationName() {
  const { t } = useTranslation("inbox")
  const groupName = useGroupDisplayName()
  const contactName = useContactName()
  return useCallback(
    (conversation: InboxConversation) => {
      if (isAgentInboxConversation(conversation))
        return `${conversation.agent.agentName} · ${conversation.agent.title}`
      if (isDirectInboxConversation(conversation)) {
        return conversation.direct.peerName.trim() || t("unknownSender")
      }
      if (isServiceInboxConversation(conversation)) {
        const { requesterName, requesterContactNumber } = conversation.service
        return contactName(requesterName, requesterContactNumber) || t("unknownSender")
      }
      if (isGroupInboxConversation(conversation)) {
        return groupName(conversation.group, conversation.group.memberCount)
      }
      return t("unknownSender")
    },
    [contactName, groupName, t],
  )
}
