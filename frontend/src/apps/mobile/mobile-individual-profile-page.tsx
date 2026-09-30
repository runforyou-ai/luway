/** 移动端单聊与 AI 会话的资料子页：对端名称、身份类型与 AI 运行状态。 */
import { useTranslation } from "react-i18next"
import { useOutletContext } from "react-router"

import { isAgentInboxConversation } from "@/api"
import type { MobileIndividualConversationContext } from "@/apps/mobile/mobile-individual-conversation-page"
import {
  MobilePageHeader,
  MobileProfileField,
  MobileScrollArea,
} from "@/apps/mobile/mobile-page"
import { ConversationAvatar } from "@/features/inbox/conversation-avatar"
import { InternalConversationDetails } from "@/features/inbox/internal-conversation-details"

/** 全屏展示会话对端资料，返回时回到所在会话。 */
export function MobileIndividualProfilePage() {
  const { t } = useTranslation("inbox")
  const { conversation } = useOutletContext<MobileIndividualConversationContext>()
  const agent = isAgentInboxConversation(conversation)
  const name =
    (agent ? conversation.agent.agentName : conversation.direct.peerName).trim() ||
    t("unknownSender")

  return (
    <section className="absolute inset-0 flex min-h-0 flex-col bg-background">
      <MobilePageHeader
        backTo={
          agent
            ? `/chats/agent/${conversation.id}`
            : `/chats/direct/${conversation.id}`
        }
        title={t("contextProfileTab")}
      />
      <MobileScrollArea
        storageKey={`individual-profile:${conversation.id}`}
        className="px-4 py-6"
      >
        <div className="flex items-center gap-3 pb-6">
          <ConversationAvatar conversation={conversation} className="size-14" />
          <h2 className="min-w-0 break-words text-lg font-semibold">{name}</h2>
        </div>
        <dl className="divide-y border-y empty:hidden">
          <InternalConversationDetails
            conversation={conversation}
            field={MobileProfileField}
          />
        </dl>
      </MobileScrollArea>
    </section>
  )
}
