/** 会话列表和会话头共用的头像、渠道角标、助理在线状态标记与负责人小头像。 */
import { useTranslation } from "react-i18next"

import {
  OrganizationIdentityType,
  isServiceInboxConversation,
  isAgentInboxConversation,
  isDirectInboxConversation,
  isGroupInboxConversation,
  type InboxConversationData,
} from "@/api"
import { AssistantPresenceMark } from "@/components/assistant-presence-mark"
import { ProfileAvatar } from "@/components/profile-avatar"
import { messageChannelTypeDefinition } from "@/lib/message-channel-types"
import { cn } from "@/lib/utils"

/** 展示会话对象头像，客户会话带来源渠道角标，助理单聊带在线状态标记；标记描边色随所在表面传入。 */
export function ConversationAvatar({
  conversation,
  className,
  compactMark = false,
  markRingClassName = "ring-background",
}: {
  conversation: InboxConversationData
  className?: string
  compactMark?: boolean
  markRingClassName?: string
}) {
  const customer = isServiceInboxConversation(conversation)
    ? conversation.service
    : null
  const direct = isDirectInboxConversation(conversation)
    ? conversation.direct
    : null
  const group = isGroupInboxConversation(conversation)
    ? conversation.group
    : null
  const agent = isAgentInboxConversation(conversation) ? conversation.agent : null
  const badge = customer?.channel
    ? messageChannelTypeDefinition(customer.channel.type)
    : undefined
  const contactName =
    customer?.requesterName?.trim() ||
    direct?.peerName.trim() || agent?.agentName.trim() ||
    group?.title.trim()
  const avatarURL =
    customer?.requesterAvatarUrl ?? direct?.peerAvatarUrl ?? agent?.agentAvatarUrl ?? group?.imageUrl
  const fallback = group
    ? "group"
    : agent
      ? "agent"
      : "person"

  return (
    <div className="relative shrink-0">
      <ProfileAvatar
        imageURL={avatarURL}
        name={contactName}
        fallback={fallback}
        seed={customer?.requesterContactNumber}
        className={className}
      />
      {badge ? (
        <span
          aria-hidden="true"
          className={cn(
            "absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full border-2 border-background text-white",
            badge.badgeClassName,
          )}
        >
          <badge.icon className="size-2" />
        </span>
      ) : (
        <AssistantPresenceMark
          presence={agent?.assistantPresence}
          compact={compactMark}
          className={cn("absolute -right-0.5 -bottom-0.5", compactMark ? "ring-[1.5px]" : "ring-2", markRingClassName)}
        />
      )}
    </div>
  )
}

/** 客户会话有负责人时展示其小头像。 */
export function ConversationAssigneeAvatar({
  conversation,
  className,
}: {
  conversation: InboxConversationData
  className?: string
}) {
  const { t } = useTranslation("inbox")
  const assignee = isServiceInboxConversation(conversation)
    ? conversation.service.assignee
    : null
  if (!assignee) return null
  const label = t("conversationAssignee", { name: assignee.displayName })
  return (
    <span role="img" aria-label={label} title={label} className="shrink-0">
      <ProfileAvatar
        imageURL={assignee.avatarUrl}
        name={assignee.displayName}
        fallback={
          assignee.type === OrganizationIdentityType.OrganizationIdentityTypeAgent
            ? "agent"
            : "person"
        }
        className={cn("rounded-sm", className)}
      />
    </span>
  )
}
