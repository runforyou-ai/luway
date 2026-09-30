/** 移动端会话列表：标题栏、会话行、置顶排序、长按菜单、加载与空状态。 */
import { useEffect, useRef, type ReactNode } from "react"
import { GripVerticalIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import {
  isServiceInboxConversation,
  isAgentInboxConversation,
  isDirectInboxConversation,
  isGroupInboxConversation,
  type ServiceInboxConversationData,
  type AgentInboxConversationData,
  type DirectInboxConversationData,
  type InboxConversationData,
  type GroupInboxConversationData,
} from "@/api"
import {
  MobilePageHeader,
  MobilePageState,
} from "@/apps/mobile/mobile-page"
import {
  mobileConversationPath,
  useMobileNavigation,
} from "@/apps/mobile/mobile-navigation"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { Button } from "@/components/ui/button"
import {
  ConversationListMenu,
  useConversationListActions,
} from "@/features/inbox/conversation-list-menu"
import { ConversationRowContent } from "@/features/inbox/conversation-row-content"
import { InboxListPanel } from "@/features/inbox/inbox-list-panel"
import {
  PinnedConversationList,
  pinMoveCommand,
  pinSortableStyle,
  usePinSortable,
  type PinSortable,
} from "@/features/inbox/pinned-sort"
import { useConversationName } from "@/hooks/use-conversation-name"
import type {
  InboxList,
  InboxListViewport,
  PartitionedInboxList,
} from "@/features/inbox/use-inbox-list"
import { useMemberChatPollingActive } from "@/features/inbox/use-member-chat-polling"
import { cn } from "@/lib/utils"

type MobileInboxConversation =
  | ServiceInboxConversationData
  | AgentInboxConversationData
  | DirectInboxConversationData
  | GroupInboxConversationData

/** 识别移动端支持的会话摘要。 */
function isMobileInboxConversation(
  conversation: InboxConversationData,
): conversation is MobileInboxConversation {
  return (
    isServiceInboxConversation(conversation) ||
    isAgentInboxConversation(conversation) ||
    isDirectInboxConversation(conversation) ||
    isGroupInboxConversation(conversation)
  )
}

type MobileConversationRowProps = {
  conversation: MobileInboxConversation
  name: string
  actions: ReturnType<typeof useConversationListActions>
  pinOrderVersion: string
  pinMoves?: NonNullable<Parameters<typeof ConversationListMenu>[0]["pinMoves"]>
  showAssignee: boolean
  showAudience: boolean
  sorting: boolean
  onMenuChange: (open: boolean) => void
  onOpen: (conversation: MobileInboxConversation) => void
  sortable?: PinSortable
}

/** 渲染会话摘要和未读角标，点击进入会话详情，长按打开阅读状态与置顶菜单；排序模式下置顶行右侧显示拖动手柄。 */
function MobileConversationRow({
  conversation,
  name,
  actions,
  pinOrderVersion,
  pinMoves,
  showAssignee,
  showAudience,
  sorting,
  onMenuChange,
  onOpen,
  sortable,
}: MobileConversationRowProps) {
  const { t } = useTranslation("inbox")
  return (
    <li
      ref={sortable?.setNodeRef}
      style={pinSortableStyle(sortable)}
      data-inbox-id={conversation.id}
      data-pinned={conversation.pinned || undefined}
      className={cn(
        "flex min-w-0 items-center border-b last:border-b-0",
        conversation.pinned && "bg-muted",
        sortable?.isDragging && "relative z-10 shadow-md",
      )}
    >
      <div className="min-w-0 flex-1">
        <ConversationListMenu
          conversation={conversation}
          actions={actions}
          itemClassName="min-h-11"
          pinOrderVersion={pinOrderVersion}
          pinMoves={pinMoves}
          onOpenChange={onMenuChange}
        >
          <button
            type="button"
            className="flex w-full min-w-0 gap-3 px-4 py-3 text-left outline-none transition-colors select-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            aria-label={name}
            onClick={() => onOpen(conversation)}
          >
            <ConversationRowContent
              conversation={conversation}
              name={name}
              density="touch"
              showAssignee={showAssignee}
              showAudience={showAudience}
            />
          </button>
        </ConversationListMenu>
      </div>
      {sorting && sortable ? (
        <button
          type="button"
          ref={sortable.setActivatorNodeRef}
          data-pin-sort-handle
          {...sortable.attributes}
          {...sortable.listeners}
          className="flex size-11 shrink-0 touch-none items-center justify-center text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          aria-label={t("pinSortHandle", { name })}
        >
          <GripVerticalIcon className="size-5" />
        </button>
      ) : null}
    </li>
  )
}

/** 置顶区内的移动端会话行，只在排序模式且未保存时可拖动。 */
function SortableMobileConversationRow(props: MobileConversationRowProps) {
  const sortable = usePinSortable(
    props.conversation.id,
    !props.sorting || props.actions.saving,
  )
  return <MobileConversationRow {...props} sortable={sortable} />
}

/** 读取移动端列表所需的身份、轮询与浏览位置选项。 */
export function useMobileListOptions() {
  const pollingActive = useMemberChatPollingActive({
    requireWindowFocus: false,
  })
  const { identity } = useMobileWorkspace()
  const { inboxWindows } = useMobileNavigation()
  return { identity, active: pollingActive, history: inboxWindows }
}

/** 渲染列表标题栏，排序模式下只保留完成按钮。 */
export function MobileListHeader({
  title,
  actions,
  sorting,
  onSortingDone,
}: {
  title: string
  actions: ReactNode
  sorting: boolean
  onSortingDone: () => void
}) {
  const { t } = useTranslation("inbox")
  return (
    <MobilePageHeader
      title={title}
      actions={sorting ? (
        <Button variant="ghost" className="-mr-2 min-h-11" onClick={onSortingDone}>
          {t("pinSortDone")}
        </Button>
      ) : actions}
    />
  )
}

/** 渲染会话列表主体：加载、错误与空状态，置顶区在前的会话行和置顶排序。 */
export function MobileConversationList({
  list,
  viewport,
  showAssignee,
  showAudience,
  emptyTitle,
  emptyDescription,
  sorting,
  onSortingChange,
}: {
  list: InboxList | PartitionedInboxList
  viewport: InboxListViewport
  showAssignee: boolean
  showAudience: boolean
  emptyTitle: string
  emptyDescription: string
  sorting: boolean
  onSortingChange: (sorting: boolean) => void
}) {
  const { t } = useTranslation("mobile")
  const conversationName = useConversationName()
  const navigate = useNavigate()
  const actions = useConversationListActions(list.settlePin)
  const exitSortingOnMenuClose = useRef(false)
  useEffect(() => {
    if (!sorting) return
    // 排序期间系统返回先退出排序模式，在捕获阶段先于一级页签的返回处理。
    const exitSorting = (event: Event) => {
      if (event.defaultPrevented) return
      event.preventDefault()
      onSortingChange(false)
    }
    window.addEventListener("app:back", exitSorting, true)
    return () => window.removeEventListener("app:back", exitSorting, true)
  }, [sorting, onSortingChange])
  const conversations = list.conversations.filter(isMobileInboxConversation)
  const names = new Map(
    conversations.map((conversation) => [
      conversation.id,
      conversationName(conversation),
    ]),
  )
  const initial = list.revision === 0
  const row = (conversation: MobileInboxConversation) => ({
    conversation,
    name: names.get(conversation.id) ?? "",
    actions,
    pinOrderVersion: list.pinOrderVersion,
    showAssignee,
    showAudience,
    sorting,
    // 排序中打开的菜单在关闭后结束排序，菜单打开期间手柄保持占位。
    onMenuChange: (open: boolean) => {
      if (open) exitSortingOnMenuClose.current = sorting
      else if (exitSortingOnMenuClose.current) onSortingChange(false)
      viewport.setMenu(open)
    },
    onOpen: (conversation: MobileInboxConversation) => {
      onSortingChange(false)
      navigate(mobileConversationPath(conversation), {
        state: { conversation, mobileBack: true },
      })
    },
  })

  return (
    <InboxListPanel list={list} viewport={viewport} mobile>
      {initial && list.error ? (
        <MobilePageState
          title={t("listLoadError")}
          onRetry={() => void list.retry()}
        />
      ) : null}
      {!initial && conversations.length === 0 && !list.hasBefore && !list.hasAfter ? (
        <MobilePageState title={emptyTitle} description={emptyDescription} />
      ) : null}
      {conversations.length > 0 ? (
        <ul>
          <PinnedConversationList
            conversations={conversations}
            pinnedIds={list.pinnedIds}
            names={names}
            pinOrderVersion={list.pinOrderVersion}
            actions={actions}
            onDraggingChange={viewport.setDragging}
            renderPinned={(conversation, order, index) => (
              <SortableMobileConversationRow
                key={conversation.id}
                {...row(conversation)}
                pinMoves={{
                  up: pinMoveCommand(order, conversation.id, index - 1, list.pinOrderVersion),
                  down: pinMoveCommand(order, conversation.id, index + 1, list.pinOrderVersion),
                  sort: () => {
                    exitSortingOnMenuClose.current = false
                    onSortingChange(true)
                  },
                }}
              />
            )}
            renderRow={(conversation) => (
              <MobileConversationRow key={conversation.id} {...row(conversation)} />
            )}
          />
        </ul>
      ) : null}
    </InboxListPanel>
  )
}
