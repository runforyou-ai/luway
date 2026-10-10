/** 移动端真人单聊与 AI 聊天详情及其会话头，真人单聊草稿与正式会话共用同一页面实例。 */
import { useState } from "react"
import { BellOffIcon, MoreHorizontalIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import {
  Navigate,
  Outlet,
  useLocation,
  useMatch,
  useNavigate,
  useParams,
} from "react-router"

import {
  ConversationType,
  isAgentInboxConversation,
  isDirectInboxConversation,
  type AgentInboxConversationData,
  type DirectInboxConversationData,
} from "@/api"
import { MobileIndividualThread } from "@/apps/mobile/chats/mobile-individual-thread"
import {
  mobileSearchPath,
  useMobileNavigation,
  type MobileLocateState,
} from "@/apps/mobile/shared/mobile-navigation"
import { MobileCoveredPage, MobilePageHeader } from "@/apps/mobile/shared/mobile-page"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { personalAgentUnavailableLabel } from "@/features/inbox/shared/agent-run-status"
import { useConversationListActions } from "@/features/inbox/list/conversation-list-menu"
import { useAccountDisabledReason } from "@/features/inbox/conversation/use-account-disabled-reason"
import { useConversationArchive } from "@/features/inbox/list/use-conversation-archive"
import { useConversationSummary } from "@/features/inbox/shared/use-conversation-summary"
import { useConversationTypingLabel } from "@/features/inbox/shared/use-conversation-typing"
import { useFirstChatMessage } from "@/features/inbox/conversation/use-first-chat-message"
import { useMountedRef } from "@/hooks/use-mounted-ref"
/** 移动端真人单聊路由状态：draftPeer 为草稿对端，handoffFrom 为首发后替换前的草稿路由编号。 */
export type MobileIndividualLocationState = MobileLocateState & {
  memberUserID?: string
  conversation?: DirectInboxConversationData
  draftPeer?: { identityId: string; displayName: string }
  handoffFrom?: string
}

/** 双方会话页向资料子页提供的会话。 */
export type MobileIndividualConversationContext = {
  conversation: DirectInboxConversationData | AgentInboxConversationData
}

/** 展示双方会话的移动端头部，静音时在标题旁显示标识：个人 AI 员工不在线时在标题栏下方整行说明，并提供包含资料、会话内搜索与归档的菜单；子页覆盖时不响应返回。 */
export function MobileIndividualHeader({
  conversation,
  peerName,
  covered = false,
}: {
  conversation: DirectInboxConversationData | AgentInboxConversationData | null
  peerName: string
  covered?: boolean
}) {
  const { t: tInbox } = useTranslation(["inbox", "contacts"])
  const { chatsURL } = useMobileNavigation()
  const location = useLocation()
  const navigate = useNavigate()
  const { memberUserID } =
    (location.state as MobileIndividualLocationState | null) ?? {}
  const typingLabel = useConversationTypingLabel(conversation?.id ?? "", null)
  // 个人 AI 员工不在线时在标题栏下方整行说明原因，正常在线不额外提示。
  const presenceLabel = personalAgentUnavailableLabel(
    conversation && isAgentInboxConversation(conversation) ? conversation.agent.personalPresence : null,
    tInbox,
  )
  const archived = Boolean(conversation?.archivedAt)
  const archive = useConversationArchive()
  const listActions = useConversationListActions()

  return (
    <>
      <MobilePageHeader
        backTo={
          covered
            ? undefined
            : memberUserID
              ? `/contacts/employees/${memberUserID}`
              : chatsURL
        }
        title={
          <span className="flex min-w-0 items-center">
            <span className="min-w-0 truncate">{typingLabel || peerName}</span>
            {conversation?.muted ? (
              <BellOffIcon
                role="img"
                className="ml-1 size-3.5 shrink-0 text-muted-foreground"
                aria-label={tInbox("conversationMuted")}
              />
            ) : null}
          </span>
        }
        actions={
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-lg"
                className="-mr-2"
                aria-label={tInbox("conversationMore")}
                disabled={!conversation}
              >
                <MoreHorizontalIcon />
              </Button>
            </DropdownMenuTrigger>
            {conversation ? (
              <DropdownMenuContent align="end" className="min-w-48">
                <DropdownMenuItem
                  className="min-h-11"
                  onSelect={() =>
                    // 资料子页挂在所在会话路由下。
                    void navigate(
                      isAgentInboxConversation(conversation)
                        ? `/chats/agent/${conversation.id}/profile`
                        : `/chats/direct/${conversation.id}/profile`,
                      { state: { conversation, mobileBack: true } },
                    )
                  }
                >
                  {tInbox("contextProfileTab")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="min-h-11"
                  onSelect={() =>
                    void navigate(
                      isAgentInboxConversation(conversation)
                        ? `/chats/agent/${conversation.id}/files`
                        : `/chats/direct/${conversation.id}/files`,
                      { state: { conversation, mobileBack: true } },
                    )
                  }
                >
                  {tInbox("contextFilesTab")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="min-h-11"
                  onSelect={() =>
                    void navigate(mobileSearchPath(conversation.id), {
                      state: { mobileBack: true },
                    })
                  }
                >
                  {tInbox("searchCurrentConversation")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="min-h-11"
                  disabled={listActions.saving}
                  onSelect={() => void listActions.toggleMuted(conversation)}
                >
                  {tInbox(conversation.muted ? "conversationUnmute" : "conversationMute")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="min-h-11"
                  disabled={archive.saving}
                  onSelect={() => void archive.save(conversation.id, !archived, conversation.pinned)}
                >
                  {tInbox(archived ? "conversationUnarchive" : "conversationArchive")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            ) : null}
          </DropdownMenu>
        }
      />
      {presenceLabel ? (
        <p
          className="shrink-0 border-b bg-sidebar px-4 py-1.5 text-center text-xs text-muted-foreground"
          role="status"
        >
          {presenceLabel}
        </p>
      ) : null}
    </>
  )
}

/** 按会话编号隔离页面状态，草稿首发后的正式会话沿用草稿编号作为实例键，保留同一实例。 */
export function MobileIndividualConversationPage() {
  const { conversationID = "" } = useParams()
  const location = useLocation()
  const [handoff, setHandoff] = useState<{ from: string; to: string } | null>(null)
  const handoffFrom = (location.state as MobileIndividualLocationState | null)?.handoffFrom
  // 记录草稿编号到正式会话编号的交接，后续进入资料子页时沿用同一实例键。
  if (handoffFrom && (handoff?.from !== handoffFrom || handoff.to !== conversationID)) {
    setHandoff({ from: handoffFrom, to: conversationID })
  }
  return (
    <MobileIndividualConversation
      key={handoff?.to === conversationID ? handoff.from : conversationID}
      conversationID={conversationID}
    />
  )
}

/** 加载并显示移动端真人单聊历史和文本发送区，草稿首发后就地切换为正式会话；资料子页打开时保留会话。 */
function MobileIndividualConversation({ conversationID }: { conversationID: string }) {
  const { t } = useTranslation(["inbox", "common"])
  const { chatsURL } = useMobileNavigation()
  const navigate = useNavigate()
  const location = useLocation()
  const childOpen = !useMatch("/chats/direct/:conversationID")
  const [draftPeer] = useState(
    () => (location.state as MobileIndividualLocationState | null)?.draftPeer ?? null,
  )
  const [created, setCreated] = useState<DirectInboxConversationData | null>(null)
  const persisted = !draftPeer || Boolean(created)
  // 首发响应确定当前会话编号，路由交接期间继续展示同一线程。
  const activeConversationID = created?.id ?? conversationID
  const alive = useMountedRef()
  const firstChat = useFirstChatMessage()
  const summary = useConversationSummary(persisted ? activeConversationID : "")
  // 路由携带或首发返回的摘要保持首屏线程，查询完成后由服务端结果接管。
  const initial = created ?? (location.state as MobileIndividualLocationState | null)?.conversation
  const data = summary.data === undefined && initial?.id === activeConversationID
    ? initial : summary.data
  const conversation = data && isDirectInboxConversation(data) ? data : null
  const disabledReason = useAccountDisabledReason(conversation)
  if (!activeConversationID) return <Navigate to={chatsURL} replace />
  const peerName =
    conversation?.direct.peerName.trim() ||
    draftPeer?.displayName ||
    t("unknownSender")

  const covered = childOpen && Boolean(conversation)

  /** 首发确认后保留页面实例，用正式会话编号替换草稿路由。 */
  function handleCreated(next: DirectInboxConversationData) {
    if (!alive.current) return
    setCreated(next)
    void navigate(`/chats/direct/${next.id}`, {
      replace: true,
      state: {
        ...(location.state as MobileIndividualLocationState | null),
        conversation: next,
        draftPeer: undefined,
        handoffFrom: conversationID,
      } satisfies MobileIndividualLocationState,
    })
  }

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
        peerName={peerName}
        covered={covered}
      />
      {!persisted && draftPeer ? (
        <MobileIndividualThread
          conversationID=""
          conversationType={ConversationType.Direct}
          peerIdentityID={draftPeer.identityId}
          onAttachmentConversationCreated={(next) => {
            if (!isDirectInboxConversation(next)) return
            firstChat.refreshStarted(next, draftPeer.identityId)
            handleCreated(next)
          }}
          sendIndividualMessage={async (input) => {
            const result = await firstChat.sendDirect(draftPeer.identityId, input)
            handleCreated(result.conversation)
            return result.message
          }}
        />
      ) : summary.loading && !conversation ? (
        <LoadingIndicator className="min-h-0 flex-1 justify-center">
          {t("messagesLoading")}
        </LoadingIndicator>
      ) : !conversation ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-sm text-muted-foreground">
          <p>{t(summary.error ? "conversationLoadError" : "conversationUnavailable")}</p>
          {summary.error ? (
            <Button
              variant="outline"
              className="min-h-11"
              onClick={() => void summary.refresh()}
            >
              {t("common:actions.retry")}
            </Button>
          ) : null}
        </div>
      ) : (
        <MobileIndividualThread
          conversationID={activeConversationID}
          conversationType={ConversationType.Direct}
          peerIdentityID={conversation.direct.peerIdentityId}
          disabledReason={disabledReason}
          enabled={!childOpen}
          lastReadMessageID={conversation.lastReadMessageId}
          locateMessage={(location.state as MobileIndividualLocationState | null)?.locateMessage}
        />
      )}
    </MobileCoveredPage>
  )
}
