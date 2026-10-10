/** 移动端 AI 对话页，在同一页面内完成草稿和正式会话的交接。 */
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Outlet,
  useLocation,
  useMatch,
  useNavigate,
  useParams,
} from "react-router"

import {
  ConversationType,
  getAgentProfile,
  getPersonalAgent,
  isAgentInboxConversation,
  isNotFoundApiError,
  UserStatus,
  type AgentData,
  type AgentInboxConversationData,
} from "@/api"
import {
  MobileIndividualHeader,
  type MobileIndividualConversationContext,
} from "@/apps/mobile/chats/mobile-individual-conversation-page"
import { MobileIndividualThread } from "@/apps/mobile/chats/mobile-individual-thread"
import type { MobileLocateState } from "@/apps/mobile/shared/mobile-navigation"
import { MobileCoveredPage, MobilePageState } from "@/apps/mobile/shared/mobile-page"
import { LoadingIndicator } from "@/components/loading-indicator"
import { useAccountDisabledReason } from "@/features/inbox/conversation/use-account-disabled-reason"
import { useConversationSummary } from "@/features/inbox/shared/use-conversation-summary"
import { useFirstChatMessage } from "@/features/inbox/conversation/use-first-chat-message"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResource } from "@/hooks/use-resource"

/** 移动端 AI 聊天草稿目标的身份、名称与账号状态。 */
type DraftTarget = Pick<AgentData, "identityId" | "displayName" | "status">

/** 移动端 AI 聊天路由状态：draftAgentID 按 AI 员工编号读取草稿目标，draftTarget 为已解析的活跃目标。 */
export type MobileAgentLocationState = MobileLocateState & {
  draftAgentID?: string
  draftTarget?: Pick<AgentData, "identityId" | "displayName">
  draftPersonalAgent?: boolean
  mobileBack?: boolean
}

/** 按会话编号隔离草稿，首发前后保留同一个聊天实例。 */
export function MobileAgentConversationPage() {
  const { conversationID = "" } = useParams()
  return (
    <MobileAgentConversation
      key={conversationID}
      conversationID={conversationID}
    />
  )
}

/** 首发后启用正式查询，保留已发送气泡和输入框直至服务端数据接管；资料子页打开时保留会话。 */
function MobileAgentConversation({ conversationID }: { conversationID: string }) {
  const { t } = useTranslation(["mobile", "inbox", "common"])
  const navigate = useNavigate()
  const location = useLocation()
  const childOpen = !useMatch("/chats/agent/:conversationID")
  const [draftAgentID] = useState(
    () => (location.state as MobileAgentLocationState | null)?.draftAgentID ?? "",
  )
  const [draftPersonalAgent] = useState(
    () => (location.state as MobileAgentLocationState | null)?.draftPersonalAgent ?? false,
  )
  const [draftAgent, setDraftAgent] = useState<DraftTarget | null>(() => {
    const target = (location.state as MobileAgentLocationState | null)?.draftTarget
    return target ? { ...target, status: UserStatus.Active } : null
  })
  const [created, setCreated] = useState<AgentInboxConversationData | null>(null)
  const persisted = (!draftAgentID && !draftAgent) || Boolean(created)
  const alive = useMountedRef()
  const firstChat = useFirstChatMessage()
  const conversationName = useConversationName()
  // 草稿目标为本人负责的个人 AI 员工时读取其详情，其余读取 AI 员工公开资料。
  const employee = useResource(
    resourceKeys.agentProfile(draftAgentID),
    () => getAgentProfile(draftAgentID),
    { staleTime: 0, enabled: !persisted && !draftAgent && !draftPersonalAgent },
  )
  const personalAgent = useResource(
    resourceKeys.personalAgent(draftAgentID),
    () => getPersonalAgent(draftAgentID),
    { staleTime: 0, enabled: !persisted && !draftAgent && draftPersonalAgent },
  )
  const agent = draftPersonalAgent
    ? { ...personalAgent, data: personalAgent.data?.personalAgent as DraftTarget | undefined }
    : employee
  const summary = useConversationSummary(persisted ? conversationID : "")
  // 首发结果只用于当前页面过渡；后续摘要（包括不可用结果）由查询接管。
  const conversation =
    summary.data === undefined
      ? created
      : summary.data && isAgentInboxConversation(summary.data)
        ? summary.data
        : null
  const disabledReason = useAccountDisabledReason(conversation)
  const resource = persisted ? summary : agent
  const ready = persisted ? Boolean(conversation) : Boolean(draftAgent)

  useEffect(() => {
    // 账号校验完成后固定草稿目标和输入区。
    if (
      !draftAgent &&
      agent.data?.status === UserStatus.Active &&
      !agent.refreshing &&
      !agent.error
    ) setDraftAgent(agent.data)
  }, [draftAgent, agent.data, agent.refreshing, agent.error])

  /** 首发后保留聊天实例并清除路由中的草稿标记。 */
  function handleCreated(conversation: AgentInboxConversationData) {
    if (!alive.current) return
    setCreated(conversation)
    void navigate(`/chats/agent/${conversationID}`, {
      replace: true,
      state: { ...location.state, draftAgentID: undefined, draftTarget: undefined },
    })
  }

  const covered = childOpen && Boolean(conversation)

  return (
    <MobileCoveredPage
      covered={covered}
      outlet={
        conversation ? (
          <Outlet
            key={conversation.id}
            context={
              { conversation } satisfies MobileIndividualConversationContext
            }
          />
        ) : null
      }
    >
      <MobileIndividualHeader
        conversation={conversation}
        covered={covered}
        peerName={
          conversation ? conversationName(conversation) :
          draftAgent?.displayName ?? agent.data?.displayName ?? t("contacts.agents")
        }
      />
      {ready ? (
        <MobileIndividualThread
          conversationID={conversationID}
          conversationType={ConversationType.Agent}
          behalfAgentName={conversation?.agent.agentName ?? null}
          enabled={persisted && !childOpen}
          attachmentAgentIdentityID={!persisted ? draftAgent?.identityId : undefined}
          onAttachmentConversationCreated={(created) => {
            if (!isAgentInboxConversation(created)) return
            firstChat.refreshStarted(created)
            handleCreated(created)
          }}
          disabledReason={disabledReason}
          lastReadMessageID={conversation?.lastReadMessageId}
          locateMessage={(location.state as MobileAgentLocationState | null)?.locateMessage}
          sendIndividualMessage={!persisted && draftAgent ? async (input) => {
            const result = await firstChat.sendAgent(conversationID, draftAgent.identityId, input)
            handleCreated(result.conversation)
            return result.message
          } : undefined}
        />
      ) : resource.loading || resource.refreshing || (!persisted && agent.data?.status === UserStatus.Active && !agent.error) ? (
        <LoadingIndicator className="min-h-0 flex-1 justify-center">
          {t("common:status.loading")}
        </LoadingIndicator>
      ) : (
        <MobilePageState
          title={persisted
            ? t(resource.error
              ? "inbox:conversationLoadError" : "inbox:conversationUnavailable")
            : t(resource.error && !isNotFoundApiError(resource.error)
              ? "agents.chatError" : "agents.unavailable")}
          onRetry={resource.error ? () => void resource.refresh() : undefined}
        />
      )}
    </MobileCoveredPage>
  )
}
