/** 移动端已有群聊的资料读取、访问恢复和详情入口。 */
import { Suspense, useCallback, useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { BellOffIcon, MoreHorizontalIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import {
  Outlet,
  useLocation,
  useMatch,
  useNavigate,
  useParams,
} from "react-router"

import { getGroupConversation, isNotFoundApiError } from "@/api"
import type { MobileGroupContext } from "@/apps/mobile/mobile-group-context"
import { MobileGroupThread } from "@/apps/mobile/mobile-group-thread"
import {
  useMobileNavigation,
  type MobileLocateState,
} from "@/apps/mobile/mobile-navigation"
import { MobilePageHeader, MobilePageState } from "@/apps/mobile/mobile-page"
import { LoadingIndicator } from "@/components/loading-indicator"
import { useRealtimeSyncActive } from "@/contexts/realtime-sync-context"
import { useAttachmentQueue } from "@/contexts/attachment-queue-context"
import { clearConversationResources } from "@/features/inbox/conversation-resources"
import { useOutgoingMessageStore } from "@/contexts/outgoing-message-context"
import { useGroupDisplayName } from "@/hooks/use-conversation-name"
import { Button } from "@/components/ui/button"
import {
  memberChatPollingInterval,
  useMemberChatPollingActive,
} from "@/features/inbox/use-member-chat-polling"
import {
  groupTypingSenderName,
  useConversationTypingLabel,
} from "@/features/inbox/use-conversation-typing"
import { resourceKeys } from "@/hooks/resource-keys"
import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"

/** 按群聊隔离资料查询、发送草稿和访问恢复状态。 */
export function MobileGroupConversationPage() {
  const { conversationID = "" } = useParams()
  return (
    <MobileGroupConversation
      key={conversationID}
      conversationID={conversationID}
    />
  )
}

/** 独立读取群资料，前台同步名称、成员人数及解散状态；失去访问权时原地显示不可用，并继续读取以便重新获得访问权后恢复。 */
function MobileGroupConversation({
  conversationID,
}: {
  conversationID: string
}) {
  const { t } = useTranslation(["mobile", "inbox", "common"])
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const groupName = useGroupDisplayName()
  const navigate = useNavigate()
  const outgoingStore = useOutgoingMessageStore()
  const queue = useAttachmentQueue()
  const location = useLocation()
  const navigationState = location.state as (MobileLocateState & {
    mobileBack?: boolean
    groupReturnDepth?: number
  }) | null
  // 记录当前群子页到来源列表的历史距离，退出时一次返回来源。
  const returnDepth =
    navigationState?.groupReturnDepth ?? (navigationState?.mobileBack ? 1 : 0)
  const { chatsURL } = useMobileNavigation()
  const invalidate = useResourceInvalidator()
  const queryClient = useQueryClient()
  const leaving = useRef(false)
  const [leavePending, setLeavePending] = useState(false)
  const detailsOpen = !useMatch("/chats/group/:conversationID")
  const pollingActive = useMemberChatPollingActive({
    requireWindowFocus: false,
  })
  const realtime = useRealtimeSyncActive()
  const previousPollingActive = useRef(pollingActive)
  const { data, loading, refreshing, error, refresh } = useResource(
    resourceKeys.groupConversation(conversationID),
    () => getGroupConversation(conversationID),
    {
      staleTime: 0,
      refetchInterval:
        pollingActive && !leavePending && !realtime
          ? memberChatPollingInterval
          : false,
      refetchOnWindowFocus: false,
    },
  )
  const [denied, setDenied] = useState(false)
  useEffect(() => {
    // 群资料以 not_found 确认不可访问，此后只在读取成功时恢复，其他读取失败保持原状态。
    if (isNotFoundApiError(error)) setDenied(true)
    else if (!error) setDenied(false)
  }, [error])
  // 当次 not_found 立即生效；主动退出期间由退出流程接管。
  const lost = (isNotFoundApiError(error) || (denied && Boolean(error))) && !leavePending
  // 详情子页打开时隐藏会话页；失去访问权后收起子页，由会话页显示不可用。
  const covered = detailsOpen && Boolean(data) && !lost
  const activityLabel = useConversationTypingLabel(
    conversationID,
    groupTypingSenderName(data?.participants ?? [], personalAgentDisplayName),
  )

  /** 消息或详情读取发现会话不可访问时重新校验群资料，由校验结果决定是否显示不可用。 */
  const handleUnavailable = useCallback(() => {
    if (!leaving.current) void refresh()
  }, [refresh])

  /** 主动退出后结束访问检测，清理该会话的本地资源并返回来源列表。 */
  function handleLeft() {
    leaving.current = true
    queue?.forgetConversation(conversationID)
    outgoingStore.forgetConversation(conversationID)
    clearConversationResources(queryClient, conversationID)
    void queryClient.resetQueries({ queryKey: resourceKeys.conversationSummary(conversationID) })
    if (returnDepth > 0) void navigate(-returnDepth)
    else void navigate(chatsURL, { replace: true })
  }

  useEffect(() => {
    // 失去访问权时清理该会话的待发内容并刷新会话列表。
    if (!lost || leaving.current) return
    queue?.forgetConversation(conversationID)
    outgoingStore.forgetConversation(conversationID)
    void invalidate(resourceKeys.inbox())
  }, [lost, conversationID, invalidate, outgoingStore, queue])

  useEffect(() => {
    // 未接入实时同步时，恢复前台立即校验群状态。
    if (
      pollingActive &&
      !previousPollingActive.current &&
      !leavePending &&
      !realtime
    )
      void refresh()
    previousPollingActive.current = pollingActive
  }, [pollingActive, leavePending, realtime, refresh])

  return (
    <div className="relative h-full min-h-0">
      <section
        className={`flex h-full min-h-0 flex-col bg-background ${covered ? "absolute inset-0 opacity-0 pointer-events-none" : ""}`}
        inert={covered}
      >
        <MobilePageHeader
          backTo={
            covered
              ? undefined
              : detailsOpen && !lost
                ? `/chats/group/${conversationID}`
                : chatsURL
          }
          title={
            detailsOpen && !lost ? (
              t("group.details")
            ) : (
              <span className="flex min-w-0 items-center">
                <span className="min-w-0 truncate">
                  {activityLabel ||
                    (data ? groupName(data, data.participants.length) : t("group.title"))}
                </span>
                {data?.muted ? (
                  <BellOffIcon
                    role="img"
                    className="ml-1 size-3.5 shrink-0 text-muted-foreground"
                    aria-label={t("inbox:conversationMuted")}
                  />
                ) : null}
              </span>
            )
          }
          actions={
            <>
              {data && error && !isNotFoundApiError(error) ? (
                <Button
                  variant="ghost"
                  className="min-h-11 text-warning"
                  disabled={refreshing}
                  onClick={() => void refresh()}
                >
                  {t("refreshFailed")}
                </Button>
              ) : null}
              <Button
                variant="ghost"
                size="icon-lg"
                className="-mr-2"
                aria-label={t("group.details")}
                disabled={!data || detailsOpen || lost}
                onClick={() =>
                  navigate(`/chats/group/${conversationID}/details`, {
                    replace: returnDepth === 0,
                    state: {
                      mobileBack: returnDepth > 0,
                      groupReturnDepth: returnDepth > 0 ? returnDepth + 1 : 0,
                    },
                  })
                }
              >
                <MoreHorizontalIcon />
              </Button>
            </>
          }
        />
        {lost ? (
          <MobilePageState title={t("group.unavailable")} />
        ) : data ? (
          <MobileGroupThread
            conversation={data}
            active={!detailsOpen}
            locateMessage={navigationState?.locateMessage}
            onUnavailable={() => {
              if (!leavePending) handleUnavailable()
            }}
          />
        ) : loading || isNotFoundApiError(error) ? (
          <LoadingIndicator className="min-h-0 flex-1 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : (
          <MobilePageState
            title={t("group.loadError")}
            onRetry={() => void refresh()}
          />
        )}
      </section>
      {data && !lost ? (
        <Suspense fallback={null}>
          {/* 子页面代码加载期间保留当前页面。 */}
          <Outlet
            context={
              {
                group: data,
                returnDepth,
                error,
                refreshing,
                refresh,
                onUnavailable: handleUnavailable,
                onLeft: handleLeft,
                onLeavingChange: setLeavePending,
              } satisfies MobileGroupContext
            }
          />
        </Suspense>
      ) : null}
    </div>
  )
}
