/** 消息页会话主区，组合会话头、消息线程和联系人上下文栏。 */
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  ConversationStatus,
  getGroupConversation,
  isAgentInboxConversation,
  isServiceInboxConversation,
  isDirectInboxConversation,
  isGroupInboxConversation,
  type AgentInboxConversationData,
  type DirectInboxConversationData,
} from "@/api"
import { useWorkspace } from "@/contexts/workspace-context"
import type { ComposerDraftBridge } from "@/features/inbox/composer/conversation-composer-types"
import { ConversationHeader } from "@/features/inbox/conversation/conversation-header"
import { ConversationSidePanel } from "@/features/inbox/conversation/conversation-side-panel"
import { ConversationThread } from "@/features/inbox/conversation/conversation-thread"
import type { ConversationLocateTarget } from "@/features/inbox/timeline/conversation-timeline"
import { customerReplyDisabledReason, useReplyWindowNow } from "@/features/inbox/service/customer-session-actions"
import { CustomerTranslationProvider } from "@/features/inbox/shared/customer-translation"
import { DirectConversationDraftHeader } from "@/features/inbox/conversation/direct-conversation-draft-header"
import { HandoffSummaryCard } from "@/features/inbox/service/handoff-summary-card"
import type { ConversationSelection } from "@/features/inbox/conversation/inbox-selection"
import { useAccountDisabledReason } from "@/features/inbox/conversation/use-account-disabled-reason"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useIsWideViewport } from "@/hooks/use-narrow-viewport"
import { useResource } from "@/hooks/use-resource"

/** 按当前选择渲染会话头、消息线程和联系人上下文栏。 */
export function ConversationMain({
  selection,
  onSessionChanged,
  onConversationChanged,
  onGroupLeft,
  onChatStarted,
  onSearchConversation,
  locateMessage,
  narrowViewport = false,
}: {
  selection: ConversationSelection
  onSessionChanged?: (conversationID: string) => void
  onConversationChanged?: (conversationID: string) => void
  onGroupLeft?: (conversationID: string) => void
  onChatStarted?: (
    conversation: DirectInboxConversationData | AgentInboxConversationData,
  ) => void
  onSearchConversation?: (conversationID: string) => void
  locateMessage: ConversationLocateTarget | null
  narrowViewport?: boolean
}) {
  const conversation =
    selection.kind === "conversation" ? selection.conversation : null
  const directTarget =
    selection.kind !== "conversation" ? selection.member : null
  const { t } = useTranslation("inbox")
  const { identity } = useWorkspace()
  const isWideViewport = useIsWideViewport()
  const conversationName = useConversationName()
  const [contextCollapsed, setContextCollapsed] = useState(
    () => !isWideViewport,
  )
  const customerDraftRef = useRef<ComposerDraftBridge | null>(null)
  const [handoffTarget, setHandoffTarget] = useState<ConversationLocateTarget | null>(null)
  // 切换会话或外部定位请求变化时以外部请求为准。
  useEffect(() => setHandoffTarget(null), [conversation?.id, locateMessage])
  const agentDraftID = selection.kind === "agent-draft" ? selection.conversationId : ""

  useEffect(() => {
    // 跨过响应式断点时恢复当前宽度对应的默认状态。
    setContextCollapsed(!isWideViewport)
  }, [isWideViewport])

  const sourceGroupConversation =
    conversation && isGroupInboxConversation(conversation) ? conversation : null
  // 群资料由会话变更通知失效。
  const groupResource = useResource(
    resourceKeys.groupConversation(sourceGroupConversation?.id ?? ""),
    () => getGroupConversation(sourceGroupConversation?.id ?? ""),
    {
      enabled: Boolean(sourceGroupConversation),
      staleTime: 0,
    },
  )
  const group = groupResource.data
  // 用群资料的标题、头像、人数和状态覆盖列表摘要。
  const displayedConversation =
    sourceGroupConversation && group
      ? {
          ...sourceGroupConversation,
          group: {
            ...sourceGroupConversation.group,
            title: group.title,
            memberPreviewNames: group.memberPreviewNames,
            imageUrl: group.imageUrl,
            memberCount: group.participants.length,
            status: group.status,
          },
        }
      : conversation
  const contactName = displayedConversation
    ? conversationName(displayedConversation)
    : (directTarget?.displayName ?? "")
  const customerConversation =
    displayedConversation && isServiceInboxConversation(displayedConversation)
      ? displayedConversation
      : null
  const directConversation =
    displayedConversation && isDirectInboxConversation(displayedConversation)
      ? displayedConversation
      : null
  const groupConversation =
    displayedConversation && isGroupInboxConversation(displayedConversation)
      ? displayedConversation
      : null
  const agentConversation =
    displayedConversation && isAgentInboxConversation(displayedConversation)
      ? displayedConversation
      : null
  const accountDisabledReason = useAccountDisabledReason(displayedConversation)
  const replyWindowNow = useReplyWindowNow(customerConversation?.service ?? null)
  const replyDisabledReason = customerConversation
    ? customerReplyDisabledReason(
        customerConversation.service,
        identity.user.identityId,
        identity.user.handlesServiceRequests,
        replyWindowNow,
        t,
      )
    : groupConversation?.group.status ===
        ConversationStatus.Archived
      ? t("groupDissolvedUnavailable")
      : accountDisabledReason
  const validConversation =
    customerConversation ??
    directConversation ??
    groupConversation ??
    agentConversation
  if (!validConversation && !directTarget) return null
  // 线程键取 AI 草稿编号、真人草稿或单聊对端身份，其余取会话编号。
  const threadKey =
    selection.kind === "agent-draft"
      ? selection.conversationId
      : (directTarget?.id ??
        directConversation?.direct.peerIdentityId ??
        validConversation?.id)

  return (
    <CustomerTranslationProvider key={customerConversation?.id ?? ""} conversationID={customerConversation?.service.channel ? customerConversation.id : null}>
    <div className="flex h-full min-h-0 bg-background">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {validConversation ? (
          <ConversationHeader
            conversation={validConversation}
            contactName={contactName}
            currentIdentityId={identity.user.identityId}
            handlesServiceRequests={identity.user.handlesServiceRequests}
            onSessionChanged={() => {
              if (customerConversation) onSessionChanged?.(customerConversation.id)
            }}
            onSearch={
              onSearchConversation
                ? () => onSearchConversation(validConversation.id)
                : undefined
            }
            groupParticipants={group?.participants}
            narrowViewport={narrowViewport}
            contextVisible={!contextCollapsed}
            onToggleContext={() => setContextCollapsed((collapsed) => !collapsed)}
          />
        ) : directTarget ? (
          <DirectConversationDraftHeader
            member={directTarget}
            narrowViewport={narrowViewport}
            contextVisible={!contextCollapsed}
            onToggleContext={() => setContextCollapsed((collapsed) => !collapsed)}
          />
        ) : null}
        {customerConversation ? (
          <HandoffSummaryCard
            key={customerConversation.id}
            conversationID={customerConversation.id}
            assignee={customerConversation.service.assignee}
            onLocateMessage={(messageId) => setHandoffTarget({ messageId, nonce: Date.now() })}
          />
        ) : null}
        <ConversationThread
          key={threadKey}
          conversation={validConversation}
          directTarget={directTarget}
          groupParticipants={group?.participants}
          agentDraftID={agentDraftID}
          replyDisabledReason={replyDisabledReason}
          onConversationChanged={() => {
            if (validConversation) onConversationChanged?.(validConversation.id)
          }}
          onChatStarted={onChatStarted}
          locateMessage={validConversation ? (handoffTarget ?? locateMessage) : null}
          customerDraftRef={customerConversation ? customerDraftRef : undefined}
        />
      </div>
      <ConversationSidePanel
        conversation={validConversation}
        directTarget={directTarget}
        displayName={contactName}
        currentIdentityID={identity.user.identityId}
        replyDisabledReason={replyDisabledReason}
        customerDraftRef={customerDraftRef}
        onGroupLeft={() => {
          if (validConversation) onGroupLeft?.(validConversation.id)
        }}
        visible={!contextCollapsed}
        onClose={() => setContextCollapsed(true)}
      />
    </div>
    </CustomerTranslationProvider>
  )
}
