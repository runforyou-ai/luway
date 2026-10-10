/** 移动端客户会话详情、回复、客户语言、AI 助手、客户资料与业务入口及客服处理周期操作。 */
import { useEffect, useRef, useState, type RefObject } from "react"
import {
  LoaderCircleIcon,
  MoreHorizontalIcon,
  SparklesIcon,
} from "lucide-react"
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
  ChannelDelivery,
  ServiceSessionStatus,
  ServiceSource,
  isServiceInboxConversation,
  type ServiceInboxConversationData,
} from "@/api"
import { MobileCustomerConversationTitle } from "@/apps/mobile/customer/mobile-customer-conversation-title"
import { MobileCustomerTransferSheet } from "@/apps/mobile/customer/mobile-customer-transfer-sheet"
import { MobileIndividualThread } from "@/apps/mobile/chats/mobile-individual-thread"
import {
  mobileSearchPath,
  useMobileBack,
  useMobileNavigation,
  type MobileLocateState,
} from "@/apps/mobile/shared/mobile-navigation"
import { MobileCoveredPage, MobilePageHeader } from "@/apps/mobile/shared/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/shared/mobile-workspace-layout"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { serviceRecipient, type ComposerDraftBridge } from "@/features/inbox/composer/conversation-composer-types"
import {
  CustomerSessionCloseDialog,
  customerReplyBlocker,
  customerReplyDisabledReason,
  useReplyWindowNow,
  useCustomerSessionActions,
} from "@/features/inbox/service/customer-session-actions"
import { CustomerTranslationProvider } from "@/features/inbox/shared/customer-translation"
import { HandoffSummaryCard } from "@/features/inbox/service/handoff-summary-card"
import { useConversationSummary } from "@/features/inbox/shared/use-conversation-summary"
import {
  customerTypingSenderName,
  useConversationTypingLabel,
} from "@/features/inbox/shared/use-conversation-typing"
import { resourceKeys } from "@/hooks/resource-keys"
import { useContactName } from "@/hooks/use-contact-name"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 客户会话页向 AI 助手、客户资料与业务子页提供的会话、对客草稿入口和回复限制。 */
export type MobileCustomerConversationContext = {
  conversation: ServiceInboxConversationData
  customerDraftRef: RefObject<ComposerDraftBridge | null>
  replyDisabledReason: string | null
}

/** 展示客户资料、业务、会话内搜索及客服处理周期的领取、接管、转交、关闭与重新打开菜单；转交在底部面板中选择去向，面板关闭后焦点在更多按钮可用时回到该按钮；关闭成功后返回来源列表。 */
function MobileCustomerSessionMenu({
  conversation,
  actions,
}: {
  conversation: ServiceInboxConversationData
  actions: ReturnType<typeof useCustomerSessionActions>
}) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const channelSource = conversation.service.source === ServiceSource.Channel
  const { operation } = actions
  const [transferOpen, setTransferOpen] = useState(false)
  const menuTriggerRef = useRef<HTMLButtonElement>(null)
  const focusAfterOperation = useRef(false)
  // 转交结束、更多按钮恢复可用后补上面板关闭时未能恢复的焦点。
  useEffect(() => {
    if (operation !== "" || !focusAfterOperation.current) return
    focusAfterOperation.current = false
    menuTriggerRef.current?.focus()
  }, [operation])

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            ref={menuTriggerRef}
            variant="ghost"
            size="icon-lg"
            className="-mr-2"
            disabled={operation !== ""}
            aria-label={t("conversationMore")}
          >
            {operation ? (
              <LoaderCircleIcon className="animate-spin" />
            ) : (
              <MoreHorizontalIcon />
            )}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-48">
          <DropdownMenuItem
            className="min-h-11"
            onSelect={() =>
              void navigate(`/inbox/customer/${conversation.id}/profile`, {
                state: { conversation, mobileBack: true },
              })
            }
          >
            {t("customerProfile")}
          </DropdownMenuItem>
          {/* 业务查询只适用于渠道来源的客户。 */}
          {channelSource ? (
            <DropdownMenuItem
              className="min-h-11"
              onSelect={() =>
                void navigate(`/inbox/customer/${conversation.id}/business`, {
                  state: { conversation, mobileBack: true },
                })
              }
            >
              {t("contextBusinessTab")}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem
            className="min-h-11"
            onSelect={() =>
              void navigate(`/inbox/customer/${conversation.id}/files`, {
                state: { conversation, mobileBack: true },
              })
            }
          >
            {t("contextFilesTab")}
          </DropdownMenuItem>
          <DropdownMenuItem
            className="min-h-11"
            onSelect={() =>
              void navigate(mobileSearchPath(conversation.id), {
                state: { mobileBack: true },
              })
            }
          >
            {t("searchCurrentConversation")}
          </DropdownMenuItem>
          {actions.reopenable ? (
            <DropdownMenuItem
              className="min-h-11"
              onSelect={() => void actions.reopen()}
            >
              {t("conversationReopen")}
            </DropdownMenuItem>
          ) : actions.claimable ? (
            <DropdownMenuItem
              className="min-h-11"
              onSelect={() => void actions.claim()}
            >
              {conversation.service.assignee
                ? t("conversationTakeover")
                : t("conversationClaim")}
            </DropdownMenuItem>
          ) : actions.transferable ? (
            <DropdownMenuItem
              className="min-h-11"
              onSelect={() => setTransferOpen(true)}
            >
              {t("conversationTransfer")}
            </DropdownMenuItem>
          ) : null}
          {actions.closable ? <DropdownMenuSeparator /> : null}
          {actions.closable ? (
            <DropdownMenuItem
              className="min-h-11 text-destructive focus:text-destructive"
              onSelect={() => actions.setCloseConfirmationOpen(true)}
            >
              {t("conversationClose")}
            </DropdownMenuItem>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>
      <MobileCustomerTransferSheet
        actions={actions}
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          const trigger = menuTriggerRef.current
          if (trigger?.disabled) focusAfterOperation.current = true
          else trigger?.focus()
        }}
      />
      <CustomerSessionCloseDialog actions={actions} />
    </>
  )
}

/** 加载客户会话摘要，展示历史、回复区、标题下方的输入状态与客户语言、AI 助手入口和处理菜单；子页打开时保留会话与草稿。 */
export function MobileCustomerConversationPage() {
  const { t } = useTranslation(["inbox", "common"])
  const { inboxURL } = useMobileNavigation()
  const { identity } = useMobileWorkspace()
  const { conversationID = "" } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const childOpen = !useMatch("/inbox/customer/:conversationID")
  const customerDraftRef = useRef<ComposerDraftBridge | null>(null)
  const summary = useConversationSummary(conversationID)
  const locationState = location.state as
    | (MobileLocateState & { conversation?: ServiceInboxConversationData })
    | null
  const [handoffTarget, setHandoffTarget] = useState<MobileLocateState["locateMessage"] | null>(null)
  // 切换会话或路由带来的定位请求变化时以路由请求为准。
  useEffect(() => setHandoffTarget(null), [conversationID, locationState?.locateMessage])
  // 路由携带的摘要保持首屏线程，查询完成后由服务端结果接管。
  const initial = locationState?.conversation
  const data =
    summary.data === undefined && initial?.id === conversationID
      ? initial
      : summary.data
  const conversation = data && isServiceInboxConversation(data) ? data : null
  const contactName = useContactName()
  const activityLabel = useConversationTypingLabel(
    conversationID,
    conversation ? customerTypingSenderName(conversation.service, contactName) : null,
  )
  const conversationName = useConversationName()
  const back = useMobileBack(inboxURL)
  const invalidate = useResourceInvalidator()
  const alive = useMountedRef()
  const sessionActions = useCustomerSessionActions(
    conversation,
    identity.user.identityId,
    identity.user.handlesServiceRequests,
    (session) => {
      void invalidate(resourceKeys.inbox())
      void invalidate(resourceKeys.conversationSummary(conversationID))
      // 离开会话页后到达的关闭结果只刷新数据，跳过导航。
      if (alive.current && session.status === ServiceSessionStatus.Closed) back()
    },
  )
  const replyWindowNow = useReplyWindowNow(conversation?.service ?? null)
  if (!conversationID) return <Navigate to={inboxURL} replace />
  const customer = conversation?.service
  const disabledReason = customer
    ? customerReplyDisabledReason(
        customer,
        identity.user.identityId,
        identity.user.handlesServiceRequests,
        replyWindowNow,
        t,
      )
    : null

  const covered = childOpen && Boolean(conversation)
  // 周期已关闭或由他人负责时，重新打开与接管同时放在输入区的原因旁。
  const blocker = customer
    ? customerReplyBlocker(customer, identity.user.identityId, identity.user.handlesServiceRequests, replyWindowNow)
    : null
  const disabledAction = blocker === "closed" && sessionActions.reopenable
    ? { label: t("conversationReopen"), busy: sessionActions.operation === "reopen", onClick: () => void sessionActions.reopen() }
    : blocker === "assigned" && sessionActions.claimable
      ? { label: t("conversationTakeover"), busy: sessionActions.operation === "claim", onClick: () => void sessionActions.claim() }
      : null

  return (
    <CustomerTranslationProvider key={conversationID} conversationID={conversation?.service.channel ? conversationID : null}>
    <MobileCoveredPage
      covered={covered}
      outlet={
        conversation ? (
          <Outlet
            key={conversation.id}
            context={
              {
                conversation,
                customerDraftRef,
                replyDisabledReason: disabledReason,
              } satisfies MobileCustomerConversationContext
            }
          />
        ) : null
      }
    >
      <MobilePageHeader
        backTo={covered ? undefined : inboxURL}
        title={
          <MobileCustomerConversationTitle
            name={conversation ? conversationName(conversation) : t("unknownSender")}
            activityLabel={activityLabel || null}
          />
        }
        actions={
          <>
            {conversation?.service.source === ServiceSource.Channel ? (
            <Button
              variant="ghost"
              size="icon-lg"
              aria-label={t("contextAssistantTab")}
              title={t("contextAssistantTab")}
              disabled={!conversation}
              onClick={() =>
                void navigate(`/inbox/customer/${conversationID}/copilot`, {
                  state: { conversation, mobileBack: true },
                })
              }
            >
              <SparklesIcon />
            </Button>
            ) : null}
            {conversation ? (
              <MobileCustomerSessionMenu conversation={conversation} actions={sessionActions} />
            ) : null}
          </>
        }
      />
      {summary.loading && !conversation ? (
        <LoadingIndicator className="min-h-0 flex-1 justify-center">
          {t("messagesLoading")}
        </LoadingIndicator>
      ) : !conversation ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-sm text-muted-foreground">
          <p>
            {t(summary.error ? "conversationLoadError" : "conversationUnavailable")}
          </p>
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
        <>
        <HandoffSummaryCard
          key={`handoff-${conversationID}`}
          conversationID={conversationID}
          assignee={conversation.service.assignee}
          onLocateMessage={(messageId) => setHandoffTarget({ messageId, nonce: Date.now() })}
        />
        <MobileIndividualThread
          key={conversationID}
          conversationID={conversationID}
          conversationType={conversation.type}
          requesterChatSubjectID={conversation.service.requesterChatSubjectId}
          customerDeliveries={
            conversation.service.channel?.capabilities.delivery === ChannelDelivery.Platform
          }
          customerAttachment={conversation.service.channel}
          serviceRecipient={serviceRecipient(conversation.service)}
          disabledReason={disabledReason}
          disabledAction={disabledAction}
          enabled={!childOpen}
          customerDraftRef={customerDraftRef}
          lastReadMessageID={conversation.lastReadMessageId}
          locateMessage={handoffTarget ?? locationState?.locateMessage}
        />
        </>
      )}
    </MobileCoveredPage>
    </CustomerTranslationProvider>
  )
}
