/** 一级栏中的群聊与单聊分节列表和发起入口。 */
import { memo, useMemo, useRef, useState, type ComponentProps } from "react"
import { ChevronDownIcon, EllipsisIcon, PencilLineIcon, PinIcon, PlusIcon, RotateCwIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate } from "react-router"

import {
  ConversationType,
  InboxPartition,
  InboxScope,
  loadInbox,
  updateConversationUnreadMark,
  type Identity,
  type InboxConversationData,
} from "@/api"
import { CountBadge } from "@/components/count-badge"
import { IconTooltip } from "@/components/icon-tooltip"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useComposerDraft } from "@/contexts/composer-draft-context"
import { ConversationAvatar } from "@/features/inbox/conversation-avatar"
import {
  ConversationListMenu,
  useConversationListActions,
} from "@/features/inbox/conversation-list-menu"
import { ConversationRowName } from "@/features/inbox/conversation-row-name"
import { ConversationTargetPickerDialog } from "@/features/inbox/conversation-target-picker-dialog"
import { CreateGroupConversationDialog } from "@/features/inbox/create-group-conversation-dialog"
import { normalizeInboxQuery } from "@/features/inbox/inbox-query"
import {
  PinnedConversationList,
  pinSortableStyle,
  usePinSortable,
  type PinSortable,
} from "@/features/inbox/pinned-sort"
import { useConversationName } from "@/hooks/use-conversation-name"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { readLocalPreference, writeLocalPreference } from "@/lib/local-preference"
import { recoverSession } from "@/lib/session-navigation"
import { cn } from "@/lib/utils"

/** 每节首屏与每次展开追加的会话数。 */
const chatSectionPageSize = 20
/** 置顶区每页读取的会话数。 */
const pinnedChatPageSize = 100
const collapsedStorageKey = "app.workspace.chat-sections-collapsed"

/** 一级栏的两个聊天分节。 */
const chatSections = [
  { id: "groups", title: "railGroups", create: "newGroupConversation", kinds: [ConversationType.ConversationTypeGroup] },
  { id: "directs", title: "railDirects", create: "newDirectConversation", kinds: [ConversationType.ConversationTypeDirect, ConversationType.ConversationTypeAgent] },
] as const

type ChatSectionId = (typeof chatSections)[number]["id"]

/** 按置顶在前、最近活动在后读取一节聊天：置顶区按页读完，普通区按 limit 条读取，加大 limit 时保留已有行直到新结果返回；enabled 为假时不读取。 */
function useChatSection(identity: Identity, kinds: readonly ConversationType[], limit: number, enabled: boolean) {
  const owner = { organizationId: identity.organization.id, userId: identity.user.id }
  const query = normalizeInboxQuery({ scope: InboxScope.InboxScopeChat, kinds: [...kinds] })
  const pinnedQuery = { ...query, partition: InboxPartition.InboxPartitionPinned }
  const regularQuery = { ...query, partition: InboxPartition.InboxPartitionRegular, limit }
  const pinned = useResource(resourceKeys.inbox({ ...owner, ...pinnedQuery, rail: true }), async () => {
    // 置顶区顺序由本人维护，数量有限，逐页读完整个置顶区。
    let page = await loadInbox({ ...pinnedQuery, limit: pinnedChatPageSize })
    const conversations = [...page.conversations]
    while (page.hasMore) {
      page = await loadInbox({ ...pinnedQuery, limit: pinnedChatPageSize, cursor: page.nextCursor })
      conversations.push(...page.conversations)
    }
    return { conversations, pinOrderVersion: page.pinOrderVersion }
  }, { enabled })
  const regular = useResource(
    resourceKeys.inbox({ ...owner, ...regularQuery, rail: true }),
    () => loadInbox(regularQuery),
    { enabled, keepPreviousData: true },
  )
  const pinnedIds = pinned.data?.conversations.map((conversation) => conversation.id) ?? []
  return {
    pinnedIds,
    conversations: [
      ...(pinned.data?.conversations ?? []),
      ...(regular.data?.conversations ?? []).filter((conversation) => !pinnedIds.includes(conversation.id)),
    ],
    hasMore: regular.data?.hasMore ?? false,
    pinOrderVersion: pinned.data?.pinOrderVersion ?? "",
    failed: Boolean(pinned.error ?? regular.error),
    retry: () => {
      if (pinned.error) void pinned.refresh()
      if (regular.error) void regular.refresh()
    },
  }
}

/** 一级栏中的一条聊天，右键菜单与列表项一致；属性不变时跳过渲染。 */
const ChatRailItem = memo(function ChatRailItem({
  conversation,
  name,
  selected,
  collapsed,
  actions,
  pinOrderVersion,
  onOpen,
  sortable,
}: {
  conversation: InboxConversationData
  name: string
  selected: boolean
  collapsed: boolean
  actions: ReturnType<typeof useConversationListActions>
  pinOrderVersion: string
  onOpen: (conversationId: string) => void
  sortable?: PinSortable
}) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const unread = conversation.unreadCount > 0 || conversation.markedUnread
  // 未选中且有未发送草稿的聊天在名称后显示草稿标记。
  const drafted = Boolean(useComposerDraft(conversation.id)) && !selected
  // 再次点击当前聊天也按进入会话处理，排在在途的手动标记之后清除。
  const open = () => {
    if (selected) {
      void updateConversationUnreadMark(conversation.id, { markedUnread: false })
        .then(() => invalidate(resourceKeys.inbox()))
        .catch((error: unknown) => {
          console.warn("清除会话未读标记失败", { conversationId: conversation.id, error })
          recoverSession(error, navigate)
        })
    }
    onOpen(conversation.id)
  }
  const item = (
    <button
      type="button"
      ref={sortable?.setNodeRef}
      style={pinSortableStyle(sortable)}
      {...sortable?.attributes}
      {...sortable?.listeners}
      aria-current={selected ? "page" : undefined}
      aria-label={name}
      className={cn(
        "flex h-8 shrink-0 items-center rounded-md text-left text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring",
        collapsed ? "relative w-8 justify-center" : "w-full gap-2 px-2.5",
        selected && "bg-sidebar-accent font-medium text-sidebar-accent-foreground",
        unread && !conversation.muted && "font-medium text-foreground",
        sortable?.isDragging && "relative z-10 bg-sidebar-accent shadow-md",
      )}
      onClick={open}
    >
      <ConversationAvatar
        conversation={conversation}
        className="size-5 shrink-0"
        compactMark
        markRingClassName={selected || sortable?.isDragging ? "ring-sidebar-accent" : "ring-sidebar"}
      />
      {collapsed ? (
        unread ? <span aria-hidden="true" className="absolute top-1 right-1 size-1.5 rounded-full bg-destructive" /> : null
      ) : (
        <>
          <ConversationRowName
            conversation={conversation}
            name={name}
            className={cn(conversation.muted && "text-muted-foreground")}
          />
          {drafted ? (
            <PencilLineIcon className="size-3 shrink-0 text-muted-foreground" aria-label={t("railDraft")} />
          ) : null}
          {conversation.mentionedUnreadCount > 0 ? (
            <span className="shrink-0 text-xs font-semibold text-destructive" aria-label={t("railMentioned")}>@</span>
          ) : null}
          {conversation.unreadCount > 0 ? (
            <CountBadge count={conversation.unreadCount} tone={conversation.muted ? "muted" : "alert"} />
          ) : conversation.markedUnread ? (
            <span className="size-2 shrink-0 rounded-full bg-destructive" aria-label={t("conversationMarkedUnread")} />
          ) : conversation.pinned && conversation.mentionedUnreadCount === 0 ? (
            <PinIcon aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
          ) : null}
        </>
      )}
    </button>
  )
  return (
    <ConversationListMenu conversation={conversation} actions={actions} pinOrderVersion={pinOrderVersion}>
      {collapsed ? (
        <Tooltip>
          <TooltipTrigger asChild>{item}</TooltipTrigger>
          <TooltipContent side="right">{name}</TooltipContent>
        </Tooltip>
      ) : item}
    </ConversationListMenu>
  )
})

/** 置顶区内可拖动与键盘排序的聊天，保存期间停用排序。 */
const SortableChatRailItem = memo(function SortableChatRailItem(props: ComponentProps<typeof ChatRailItem>) {
  const sortable = usePinSortable(props.conversation.id, props.actions.saving)
  return <ChatRailItem {...props} sortable={sortable} />
})

/** 一节聊天：标题行可收起并带发起入口，末尾可继续展开更早的聊天。 */
function ChatRailSection({
  identity,
  section,
  sectionCollapsed,
  railCollapsed,
  selectedConversationId,
  onToggle,
  onCreate,
  onOpen,
}: {
  identity: Identity
  section: (typeof chatSections)[number]
  sectionCollapsed: boolean
  railCollapsed: boolean
  selectedConversationId: string
  onToggle: () => void
  onCreate: () => void
  onOpen: (conversationId: string) => void
}) {
  const { t } = useTranslation("inbox")
  const invalidate = useResourceInvalidator()
  const [limit, setLimit] = useState(chatSectionPageSize)
  // 展开的侧栏中已收起的分节不展示会话，暂停读取。
  const chats = useChatSection(identity, section.kinds, limit, railCollapsed || !sectionCollapsed)
  const actions = useConversationListActions(async () => {
    await invalidate(resourceKeys.inbox())
  })
  const conversationName = useConversationName()
  // 列表项只接收引用稳定的打开回调，回调内调用本次渲染的最新实现。
  const openRef = useRef(onOpen)
  openRef.current = onOpen
  const open = useMemo(() => (conversationId: string) => openRef.current(conversationId), [])
  const names = new Map(chats.conversations.map((conversation) => [conversation.id, conversationName(conversation)]))
  const itemProps = (conversation: InboxConversationData) => ({
    conversation,
    name: names.get(conversation.id) ?? "",
    selected: conversation.id === selectedConversationId,
    collapsed: railCollapsed,
    actions,
    pinOrderVersion: chats.pinOrderVersion,
    onOpen: open,
  })
  // 置顶聊天在前，可拖动调整顺序，与原会话列表的置顶排序一致。
  const items = (
    <PinnedConversationList
      conversations={chats.conversations}
      pinnedIds={chats.pinnedIds}
      names={names}
      pinOrderVersion={chats.pinOrderVersion}
      actions={actions}
      renderPinned={(conversation) => <SortableChatRailItem key={conversation.id} {...itemProps(conversation)} />}
      renderRow={(conversation) => <ChatRailItem key={conversation.id} {...itemProps(conversation)} />}
      separator={
        <div aria-hidden="true" className={cn("h-px shrink-0 bg-sidebar-border", railCollapsed ? "w-5" : "mx-2.5 my-1")} />
      }
    />
  )

  const showMore = () => setLimit((current) => current + chatSectionPageSize)
  const failure = chats.failed ? (
    <button
      type="button"
      className={cn(
        "flex h-7 items-center rounded-md text-xs text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
        railCollapsed ? "w-8 justify-center" : "w-full gap-1.5 px-2.5",
      )}
      title={railCollapsed ? t("railLoadError") : undefined}
      aria-label={t("railLoadError")}
      onClick={chats.retry}
    >
      <RotateCwIcon className="size-3.5 shrink-0" />
      {railCollapsed ? null : <span className="truncate">{t("railLoadError")}</span>}
    </button>
  ) : null

  if (railCollapsed) {
    return (
      <div className="flex flex-col items-center gap-1.5 border-t border-sidebar-border pt-1.5">
        {items}
        {failure}
        {chats.hasMore ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-label={t("railShowMore")}
                className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                onClick={showMore}
              >
                <EllipsisIcon className="size-4" />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">{t("railShowMore")}</TooltipContent>
          </Tooltip>
        ) : null}
      </div>
    )
  }
  return (
    <section aria-label={t(section.title)} className="flex flex-col gap-0.5">
      <div className="flex h-7 items-center gap-1 pr-1 pl-1">
        <button
          type="button"
          aria-expanded={!sectionCollapsed}
          className="flex h-6 min-w-0 flex-1 items-center gap-1 rounded-md px-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
          onClick={onToggle}
        >
          <ChevronDownIcon className={cn("size-3.5 shrink-0 transition-transform", sectionCollapsed && "-rotate-90")} />
          <span className="truncate">{t(section.title)}</span>
        </button>
        <IconTooltip label={t(section.create)}>
          <button
            type="button"
            aria-label={t(section.create)}
            className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
            onClick={onCreate}
          >
            <PlusIcon className="size-3.5" />
          </button>
        </IconTooltip>
      </div>
      {sectionCollapsed ? null : (
        <>
          {items}
          {failure}
          {chats.hasMore ? (
            <button
              type="button"
              className="flex h-7 w-full items-center rounded-md px-2.5 text-left text-xs text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
              onClick={showMore}
            >
              {t("railShowMore")}
            </button>
          ) : null}
        </>
      )}
    </section>
  )
}

/** 一级栏的群聊与单聊分节，窄栏下只显示头像并把发起入口合并为一个菜单；发起群聊后打开新群，发起单聊按所选成员或 AI 员工打开聊天。 */
export function ChatRailSections({
  identity,
  collapsed,
}: {
  identity: Identity
  collapsed: boolean
}) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const location = useLocation()
  const [collapsedSections, setCollapsedSections] = useState<ChatSectionId[]>(() => {
    // 读取本机记录的已收起分节。
    const stored = readLocalPreference(collapsedStorageKey)
    return Array.isArray(stored) ? stored.filter((id): id is ChatSectionId => chatSections.some((section) => section.id === id)) : []
  })
  const [groupDialogOpen, setGroupDialogOpen] = useState(false)
  const [directDialogOpen, setDirectDialogOpen] = useState(false)
  const selectedConversationId =
    location.pathname === "/chats" ? (new URLSearchParams(location.search).get("conversation") ?? "") : ""

  /** 切换一节的收起状态并记在本机。 */
  function toggleSection(id: ChatSectionId) {
    const next = collapsedSections.includes(id)
      ? collapsedSections.filter((item) => item !== id)
      : [...collapsedSections, id]
    setCollapsedSections(next)
    writeLocalPreference(collapsedStorageKey, next)
  }

  return (
    <>
      {collapsed ? (
        // 窄栏下两节不显示标题行，发起入口合并为一个菜单，放在聊天之前，不随聊天增多滚出视口。
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger asChild>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  aria-label={t("railNewChat")}
                  className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                >
                  <PlusIcon className="size-4" />
                </button>
              </DropdownMenuTrigger>
            </TooltipTrigger>
            <TooltipContent side="right">{t("railNewChat")}</TooltipContent>
          </Tooltip>
          <DropdownMenuContent side="right" align="start">
            <DropdownMenuItem onSelect={() => setGroupDialogOpen(true)}>{t("newGroupConversation")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setDirectDialogOpen(true)}>{t("newDirectConversation")}</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
      {chatSections.map((section) => (
        <ChatRailSection
          key={section.id}
          identity={identity}
          section={section}
          sectionCollapsed={collapsedSections.includes(section.id)}
          railCollapsed={collapsed}
          selectedConversationId={selectedConversationId}
          onToggle={() => toggleSection(section.id)}
          onCreate={() => (section.id === "groups" ? setGroupDialogOpen(true) : setDirectDialogOpen(true))}
          onOpen={(conversationId) => navigate(`/chats?conversation=${conversationId}`)}
        />
      ))}
      <CreateGroupConversationDialog
        open={groupDialogOpen}
        currentIdentityID={identity.user.identityId}
        onOpenChange={setGroupDialogOpen}
        onCreated={(conversation) => navigate(`/chats?conversation=${conversation.id}`)}
      />
      <ConversationTargetPickerDialog
        open={directDialogOpen}
        currentIdentityId={identity.user.identityId}
        onOpenChange={setDirectDialogOpen}
        onSelected={(member) => navigate(`/chats?target=${member.id}`)}
      />
    </>
  )
}
