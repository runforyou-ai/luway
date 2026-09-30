/** 移动端真人、AI、服务会话与群聊共用的时间线、阅读进度、文本与附件发送、服务会话内部备注和失败重试。 */
import type { ComposerDisabledAction, ComposerDraftBridge, CustomerChannelCapabilities, ServiceRecipient } from "@/features/inbox/conversation-composer-types"
import { useEffect, type RefObject } from "react"

import {
  ConversationType,
  type ConversationMessageData,
  type DirectTextMessageInput,
  type GroupParticipant,
  type InboxConversationData,
} from "@/api"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import {
  ConversationComposer,
} from "@/features/inbox/conversation-composer"
import {
  ConversationTimeline,
  type ConversationLocateTarget,
} from "@/features/inbox/conversation-timeline"
import { listAllMemberOptions } from "@/features/inbox/list-all-member-options"
import { useConversationReadMarker } from "@/features/inbox/use-conversation-read-marker"
import { useRecentConversations } from "@/features/inbox/use-recent-conversations"
import { useThreadComposerBridge } from "@/features/inbox/use-thread-composer-bridge"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"

/**
 * 草稿只展示本地发送状态，正式会话读取历史、推进已读、按需定位原消息并在前台轮询；
 * closedNotice 非空时保留历史并以该提示替换发送区（如群聊已解散）；
 * 客户会话可切换内部备注，不能对客回复时仍可写备注并引用消息；customerDraftRef 供 AI 助手读取和替换对客草稿。
 */
export function MobileIndividualThread({
  conversationID,
  conversationType = ConversationType.ConversationTypeDirect,
  requesterChatSubjectID = null,
  behalfAgentName = null,
  peerIdentityID = "",
  sendIndividualMessage,
  attachmentAgentIdentityID,
  onAttachmentConversationCreated,
  enabled = Boolean(conversationID),
  disabledReason = null,
  disabledAction = null,
  closedNotice = null,
  lastReadMessageID = null,
  customerDeliveries = false,
  customerAttachment = null,
  serviceRecipient = null,
  groupParticipants,
  locateMessage = null,
  onUnavailable,
  customerDraftRef,
}: {
  conversationID: string
  conversationType?: ConversationType
  /** 处理方查看服务会话时为发起人聊天主体编号，回复与内部备注走服务会话发送。 */
  requesterChatSubjectID?: string | null
  /** 发起人查看服务聊天时为接待的 AI 员工名称。 */
  behalfAgentName?: string | null
  peerIdentityID?: string
  attachmentAgentIdentityID?: string
  onAttachmentConversationCreated?: (conversation: InboxConversationData) => void
  enabled?: boolean
  disabledReason?: string | null
  /** 不能回复时在原因旁提供的操作，例如接管或重新打开。 */
  disabledAction?: ComposerDisabledAction | null
  closedNotice?: string | null
  lastReadMessageID?: string | null
  customerDeliveries?: boolean
  customerAttachment?: CustomerChannelCapabilities | null
  /** 处理方查看服务会话时对客回复的接收方。 */
  serviceRecipient?: ServiceRecipient | null
  groupParticipants?: GroupParticipant[]
  locateMessage?: ConversationLocateTarget | null
  onUnavailable?: () => void
  customerDraftRef?: RefObject<ComposerDraftBridge | null>
  sendIndividualMessage?: (
    input: DirectTextMessageInput,
  ) => Promise<ConversationMessageData>
}) {
  const { identity } = useMobileWorkspace()
  const invalidate = useResourceInvalidator()
  // 真人草稿尚无会话编号，发送状态按对端身份分组。
  const bridge = useThreadComposerBridge(
    conversationID,
    peerIdentityID ? `draft:${peerIdentityID}` : "",
  )
  // 客户会话没有手动未读标记，进入时只推进已读水位。
  const markRead = useConversationReadMarker(
    conversationID,
    enabled && conversationType !== ConversationType.ConversationTypeChannel,
  )
  const replyDisabled = Boolean(disabledReason || closedNotice)
  const group = conversationType === ConversationType.ConversationTypeGroup
  const customer = requesterChatSubjectID !== null
  // 客户会话的内部备注可以提醒企业成员。
  const noteMentionMembers = useResource(resourceKeys.memberOptions(), listAllMemberOptions, {
    enabled: customer,
  })
  const { record: recordRecentConversation } = useRecentConversations(
    identity.user.identityId,
  )

  useEffect(() => {
    // 正式会话打开后记入本机最近打开。
    if (conversationID && enabled) recordRecentConversation(conversationID)
  }, [conversationID, enabled, recordRecentConversation])

  return (
    <>
      <ConversationTimeline
        {...bridge.timeline}
        conversationID={conversationID}
        conversationType={conversationType}
        requesterChatSubjectID={requesterChatSubjectID}
        behalfAgentName={behalfAgentName}
        currentUser={identity.user}
        requireWindowFocus={false}
        customerDeliveries={customerDeliveries}
        enabled={enabled}
        onUnavailable={onUnavailable}
        onReadMessage={enabled ? markRead : undefined}
        onReplyMessage={
          enabled && !closedNotice && (!disabledReason || customer)
            ? bridge.selectReplyTarget
            : undefined
        }
        noteReplyEnabled={customer}
        customerReplyUnavailable={replyDisabled}
        replyVisibility={bridge.visibility}
        readThroughMessageID={lastReadMessageID}
        retryFailedMessageDisabled={replyDisabled}
        locateMessage={locateMessage}
      />
      {closedNotice ? (
        <div
          className="shrink-0 border-t p-4 text-center text-sm text-muted-foreground"
          role="status"
        >
          {closedNotice}
        </div>
      ) : (
        <ConversationComposer
          {...bridge.composer}
          attachmentTargetIdentityID={!conversationID ? peerIdentityID : undefined}
          attachmentAgentDraft={attachmentAgentIdentityID
            ? { conversationID, agentIdentityID: attachmentAgentIdentityID }
            : undefined}
          onAttachmentConversationCreated={(created) => {
            if (created) onAttachmentConversationCreated?.(created)
          }}
          conversationID={conversationID}
          conversationType={conversationType}
          service={customer}
          serviceRecipient={serviceRecipient}
          currentIdentityID={identity.user.identityId}
          disabledReason={disabledReason}
          disabledAction={disabledAction}
          groupParticipants={groupParticipants}
          noteMentionMembers={noteMentionMembers.data}
          onVisibilityChange={customer ? bridge.setVisibility : undefined}
          sendIndividualMessage={sendIndividualMessage}
          customerChannel={customerAttachment}
          draftBridgeRef={customerDraftRef}
          onSucceeded={() => {
            void invalidate(resourceKeys.inbox())
            // 发送结果可能改变客服负责人与处理状态，同时刷新会话摘要。
            if (conversationID && !group)
              void invalidate(resourceKeys.conversationSummary(conversationID))
          }}
          onFailed={(clientMessageID) => {
            bridge.outgoing.fail(clientMessageID)
            // 发送被拒绝后同步会话摘要；群聊同时同步群状态，及时关闭已解散群的发送区。
            if (conversationID) void invalidate(resourceKeys.conversationSummary(conversationID))
            if (group) void invalidate(resourceKeys.groupConversation(conversationID))
          }}
        />
      )}
    </>
  )
}
