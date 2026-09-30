/** 移动端真人单聊与 AI 聊天详情及其会话头。 */
import { Suspense } from "react"
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
  OrganizationIdentityType,
  isAgentInboxConversation,
  isDirectInboxConversation,
  type AgentInboxConversationData,
  type DirectInboxConversationData,
} from "@/api"
import { MobileIndividualThread } from "@/apps/mobile/mobile-individual-thread"
import { MobilePageHeader } from "@/apps/mobile/mobile-page"
import {
  mobileSearchPath,
  useMobileNavigation,
  type MobileLocateState,
} from "@/apps/mobile/mobile-navigation"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { assistantUnavailableLabel } from "@/features/inbox/agent-run-status"
import { useConversationSummary } from "@/features/inbox/use-conversation-summary"
import { LoadingIndicator } from "@/components/loading-indicator"
import { useAccountDisabledReason } from "@/features/inbox/use-account-disabled-reason"
import { useConversationTypingLabel } from "@/features/inbox/use-conversation-typing"
import { useConversationArchive } from "@/features/inbox/use-conversation-archive"
import { useConversationListActions } from "@/features/inbox/conversation-list-menu"
type MobileIndividualLocationState = MobileLocateState & {
  memberUserID?: string
  conversation?: DirectInboxConversationData
}

/** 双方会话页向资料子页提供的会话。 */
export type MobileIndividualConversationContext = {
  conversation: DirectInboxConversationData | AgentInboxConversationData
}

/** 展示双方会话的移动端头部，静音时在标题旁显示标识：助理不在线时在标题栏下方整行说明，并提供包含资料、会话内搜索与归档的菜单；子页覆盖时不响应返回。 */
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
  const assistant =
    conversation &&
    isAgentInboxConversation(conversation) &&
    conversation.agent.agentType ===
      OrganizationIdentityType.OrganizationIdentityTypeAssistant
      ? conversation.agent
      : null
  // 助理不在线时在标题栏下方整行说明原因，正常在线不额外提示。
  const presenceLabel = assistantUnavailableLabel(assistant?.assistantPresence, tInbox)
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

/** 加载并显示移动端真人单聊历史和文本发送区；资料子页打开时保留会话。 */
export function MobileIndividualConversationPage() {
  const { t } = useTranslation(["inbox", "common"])
  const { chatsURL } = useMobileNavigation()
  const { conversationID = "" } = useParams()
  const location = useLocation()
  const childOpen = !useMatch("/chats/direct/:conversationID")
  const summary = useConversationSummary(conversationID, false)
  // 路由携带的摘要保持首屏线程，查询完成后由服务端结果接管。
  const initial = (location.state as MobileIndividualLocationState | null)?.conversation
  const data = summary.data === undefined && initial?.id === conversationID
    ? initial : summary.data
  const conversation = data && isDirectInboxConversation(data) ? data : null
  const disabledReason = useAccountDisabledReason(conversation)
  if (!conversationID) return <Navigate to={chatsURL} replace />
  const peerName =
    conversation?.direct.peerName.trim() ||
    t("unknownSender")

  const covered = childOpen && Boolean(conversation)

  return (
    <div className="relative h-full min-h-0">
      <section
        className={`flex h-full min-h-0 flex-col bg-background ${covered ? "absolute inset-0 opacity-0 pointer-events-none" : ""}`}
        inert={covered}
      >
        <MobileIndividualHeader
          conversation={conversation}
          peerName={peerName}
          covered={covered}
        />
        {summary.loading && !conversation ? (
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
            key={conversationID}
            conversationID={conversationID}
            conversationType={ConversationType.ConversationTypeDirect}
            peerIdentityID={conversation.direct.peerIdentityId}
            disabledReason={disabledReason}
            enabled={!childOpen}
            lastReadMessageID={conversation.lastReadMessageId}
            locateMessage={(location.state as MobileIndividualLocationState | null)?.locateMessage}
          />
        )}
      </section>
      {conversation ? (
        <Suspense fallback={null}>
          {/* 子页面代码加载期间保留当前页面。 */}
          <Outlet
            key={conversation.id}
            context={
              { conversation } satisfies MobileIndividualConversationContext
            }
          />
        </Suspense>
      ) : null}
    </div>
  )
}
