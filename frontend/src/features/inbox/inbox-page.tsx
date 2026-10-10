/** 收件箱中栏（服务会话列表）和会话主区。 */
import { useEffect, useState } from "react"
import { InboxIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  ServiceQueueFilter,
  InboxPendingKind,
  InboxScope,
  ServiceSessionStatus,
  isApiError,
  isServiceInboxConversation,
  listServiceAssignees,
  listInboxChannels,
  listServiceQueueTeams,
  type InboxConversation,
  type InboxQuery,
} from "@/api"
import { PageSplit } from "@/components/page-split"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useGlobalSearch } from "@/features/inbox/state/global-search-context"
import { useWorkspace } from "@/contexts/workspace-context"
import { useWorkspaceScope } from "@/contexts/workspace-scope-context"
import { ConversationDetail } from "@/features/inbox/conversation/conversation-detail"
import type { ConversationLocateTarget } from "@/features/inbox/timeline/conversation-timeline"
import { useInboxAttention } from "@/features/inbox/list/inbox-attention"
import { InboxConversationList } from "@/features/inbox/list/inbox-conversation-list"
import { InboxFilter } from "@/features/inbox/list/inbox-filter"
import { InboxListPanel } from "@/features/inbox/list/inbox-list-panel"
import { InboxPaneTop } from "@/features/inbox/list/inbox-pane-top"
import type { InboxTab, NormalizedInboxQuery } from "@/features/inbox/list/inbox-query"
import { useConversationSummary } from "@/features/inbox/shared/use-conversation-summary"
import type { InboxList, PartitionedInboxList } from "@/features/inbox/list/use-inbox-list"
import type { useInboxListViewport } from "@/features/inbox/list/use-inbox-list-viewport"
import { useOpenedConversation } from "@/features/inbox/conversation/use-opened-conversation"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConversationName } from "@/hooks/use-conversation-name"
import { useIsNarrowViewport } from "@/hooks/use-narrow-viewport"
import { useResource } from "@/hooks/use-resource"
import { focusDialogContainer } from "@/lib/dialog-focus"
import { apiErrorMessage } from "@/lib/form-errors"
import { resolveAppPlatform } from "@/platform/app-platform"
import { openConversationWindow } from "@/platform/native"

/** 收件箱筛选与选中会话的地址变更。 */
export type InboxQueryChange = Partial<InboxQuery> & {
  conversationId?: string
  replace?: boolean
}

/** 收件箱中栏和当前会话。 */
export function InboxPage({
  list,
  listViewport,
  query,
  selectedConversationId,
  locateMessage,
  onSelectedConversationChange,
  onTabChange,
  onQueryChange,
}: {
  list: InboxList | PartitionedInboxList
  listViewport: ReturnType<typeof useInboxListViewport>
  query: NormalizedInboxQuery
  selectedConversationId: string
  locateMessage: ConversationLocateTarget | null
  onSelectedConversationChange: (
    conversationId: string,
    replace?: boolean,
  ) => void
  onTabChange: (tab: InboxTab) => void
  onQueryChange: (changes: InboxQueryChange) => void
}) {
  const { conversations } = list
  // 根据当前窗口、前后分页资格和首次读取状态判断会话列表是否有效。
  const hasConversations = conversations.length > 0 || list.hasBefore || list.hasAfter || list.revision === 0
  const { t } = useTranslation(["inbox", "common"])
  const { identity } = useWorkspace()
  const workspaceScope = useWorkspaceScope()
  const pendingCount = useInboxAttention(identity).data?.pending ?? 0
  const isNarrowViewport = useIsNarrowViewport()
  const globalSearch = useGlobalSearch()
  const [isNarrowDetailOpen, setIsNarrowDetailOpen] = useState(false)
  const summary = useConversationSummary(selectedConversationId)
  const selectedConversation = summary.data ?? undefined
  const openedConversationId = selectedConversation?.id
  const [messageTarget, setMessageTarget] = useState<ConversationLocateTarget | null>(locateMessage)
  // 搜索结果带来的定位目标随地址进入，切换会话时由选择动作清除。
  const [enteredLocate, setEnteredLocate] = useState(locateMessage)
  if (enteredLocate !== locateMessage) {
    setEnteredLocate(locateMessage)
    setMessageTarget(locateMessage)
  }
  const pendingTab = query.scope === InboxScope.Pending
  useOpenedConversation(identity.user.identityId, openedConversationId)
  useEffect(() => {
    if (isNarrowViewport && selectedConversationId) setIsNarrowDetailOpen(true)
  }, [isNarrowViewport, selectedConversationId])
  useEffect(() => {
    listViewport.setCovered(isNarrowViewport && isNarrowDetailOpen)
    return () => listViewport.setCovered(false)
  }, [isNarrowViewport, isNarrowDetailOpen, listViewport])
  const conversationName = useConversationName()
  const { data: assignees = [] } = useResource(
    resourceKeys.serviceAssignees(),
    () => listServiceAssignees(),
    { enabled: !pendingTab },
  )
  const { data: queueTeams = [] } = useResource(
    resourceKeys.serviceQueueTeams(),
    () => listServiceQueueTeams(),
    { enabled: query.pendingKind === InboxPendingKind.Queue },
  )
  const { data: channels = [] } = useResource(
    resourceKeys.inboxChannels(),
    () => listInboxChannels(),
    { staleTime: 0 },
  )

  /** 选中一个会话。 */
  function selectConversation(conversationId: string) {
    setMessageTarget(null)
    onSelectedConversationChange(conversationId)
    if (isNarrowViewport) {
      setIsNarrowDetailOpen(true)
    }
  }

  /** 从会话头进入当前会话的搜索范围。 */
  function searchConversation(conversationID: string) {
    globalSearch?.open(conversationID)
  }

  /** 桌面端在独立窗口打开会话，窗口标题取会话名称。 */
  async function openConversationInWindow(conversation: InboxConversation, name: string) {
    try {
      await openConversationWindow({ workspaceSlug: workspaceScope.current.slug, conversationId: conversation.id, title: name })
    } catch (error) {
      console.warn("打开会话独立窗口失败", { conversationId: conversation.id, error })
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("conversationWindowOpenError"))
    }
  }

  /** 本人关闭服务会话后取消选中并清空主区，已关闭会话里的其他操作保持选中。 */
  function followCustomerConversation(conversation: InboxConversation) {
    if (!isServiceInboxConversation(conversation) || conversation.id !== selectedConversationId) return
    const closing =
      conversation.service.serviceSessionStatus === ServiceSessionStatus.Closed &&
      selectedConversation !== undefined &&
      isServiceInboxConversation(selectedConversation) &&
      selectedConversation.service.serviceSessionStatus === ServiceSessionStatus.Open
    if (!closing) return
    setIsNarrowDetailOpen(false)
    onQueryChange({ conversationId: "" })
  }

  /** 渲染选中会话的主区内容。 */
  function renderConversation(narrowViewport: boolean) {
    if (!selectedConversationId) return null
    return (
      <ConversationDetail
        summary={summary}
        onGroupLeft={() => onSelectedConversationChange("", true)}
        onLocalChange={followCustomerConversation}
        onSearchConversation={searchConversation}
        locateMessage={messageTarget}
        narrowViewport={narrowViewport}
      />
    )
  }

  const pane = (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <InboxPaneTop
        tab={query.scope as InboxTab}
        pendingCount={pendingCount}
        onTabChange={onTabChange}
        filter={
          <InboxFilter
            query={query}
            channels={channels}
            queueTeams={queueTeams}
            assignees={assignees}
            currentIdentityId={identity.user.identityId}
            onChange={(changes) =>
              onQueryChange({
                ...changes,
                // 切换条目类型时队列筛选回到全部队列。
                ...(changes.pendingKind !== undefined
                  ? { queueFilter: ServiceQueueFilter.All, queueTeamId: "" }
                  : {}),
              })
            }
          />
        }
      />
      <InboxListPanel list={list} viewport={listViewport} detailError={Boolean(summary.error)} retryDetail={() => void summary.refresh()}>
        <InboxConversationList
          conversations={conversations}
          showAudience
          showAssignee={!pendingTab}
          pinnedIds={list.pinnedIds}
          pinOrderVersion={list.pinOrderVersion}
          onMenuChange={listViewport.setMenu}
          onDraggingChange={listViewport.setDragging}
          onPinSettled={list.settlePin}
          selectedId={selectedConversationId || undefined}
          onSelect={selectConversation}
          onOpenInWindow={
            resolveAppPlatform() === "desktop"
              ? (conversation, name) => void openConversationInWindow(conversation, name)
              : undefined
          }
        />
      </InboxListPanel>
    </div>
  )

  return (
    <>
      <PageSplit
        paneOnNarrow="fill"
        paneVariant="nav"
        // 会话列表底色与通讯录、渠道、知识库的二级菜单一致，正文沿用主文字色。
        paneClassName="text-foreground"
        pane={pane}
      >
        {isNarrowViewport ? null : selectedConversationId ? (
          <section className="flex min-h-0 flex-1 flex-col">
            {renderConversation(false)}
          </section>
        ) : (
          <div className="app-inbox-empty-main flex min-h-0 flex-1 items-center justify-center p-6">
            <div
              data-slot="empty-state-content"
              className="max-w-sm text-center"
            >
              <div className="mx-auto mb-4 flex size-11 items-center justify-center rounded-xl border bg-background shadow-sm">
                <InboxIcon className="size-5 text-muted-foreground" />
              </div>
              <h2 className="text-base font-semibold tracking-tight">
                {t(hasConversations ? "selectConversationTitle" : pendingTab ? "pendingEmptyTitle" : "emptyTitle")}
              </h2>
              <p className="mt-2 text-sm text-muted-foreground">
                {t(hasConversations ? "selectConversationDescription" : pendingTab ? "pendingEmptyDescription" : "emptyDescription")}
              </p>
            </div>
          </div>
        )}
      </PageSplit>

      {isNarrowViewport && selectedConversationId ? (
        <Sheet open={isNarrowDetailOpen} onOpenChange={setIsNarrowDetailOpen}>
          <SheetContent
            className="data-[side=right]:w-full p-0 sm:max-w-lg"
            onOpenAutoFocus={focusDialogContainer}
          >
            <SheetHeader className="sr-only">
              <SheetTitle>
                {t("conversationTitle", {
                  name: selectedConversation ? conversationName(selectedConversation) : "",
                })}
              </SheetTitle>
              <SheetDescription>{t("detailDescription")}</SheetDescription>
            </SheetHeader>
            {renderConversation(true)}
          </SheetContent>
        </Sheet>
      ) : null}
    </>
  )
}
