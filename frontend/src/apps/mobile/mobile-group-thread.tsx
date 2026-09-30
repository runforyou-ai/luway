/** 移动端群聊线程：沿用通用线程，附带群成员提及和解散后的只读提示。 */
import { useTranslation } from "react-i18next"

import {
  ConversationStatus,
  ConversationType,
  type GroupConversationData,
} from "@/api"
import { MobileIndividualThread } from "@/apps/mobile/mobile-individual-thread"
import type { ConversationLocateTarget } from "@/features/inbox/conversation-timeline"

/** 复用消息窗口、已读与提及导航，按需定位原消息，群聊解散后保留历史并关闭发送区。 */
export function MobileGroupThread({
  conversation,
  active = true,
  locateMessage = null,
  onUnavailable,
}: {
  conversation: GroupConversationData
  active?: boolean
  locateMessage?: ConversationLocateTarget | null
  onUnavailable: () => void
}) {
  const { t } = useTranslation("mobile")
  const archived =
    conversation.status === ConversationStatus.ConversationStatusArchived
  return (
    <MobileIndividualThread
      conversationID={conversation.id}
      conversationType={ConversationType.ConversationTypeGroup}
      enabled={active}
      closedNotice={archived ? t("group.archived") : null}
      groupParticipants={conversation.participants}
      locateMessage={locateMessage}
      onUnavailable={onUnavailable}
    />
  )
}
