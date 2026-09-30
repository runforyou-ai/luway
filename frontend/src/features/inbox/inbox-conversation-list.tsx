/** 消息页中栏的会话列表项、右键操作与置顶区排序。 */
import { memo, useLayoutEffect, useMemo, useRef } from "react"
import { useNavigate } from "react-router"

import {
  isInternalInboxConversation,
  updateConversationUnreadMark,
  type InboxConversationData,
} from "@/api"
import {
  ConversationListMenu,
  useConversationListActions,
} from "@/features/inbox/conversation-list-menu"
import { inboxConversationSummary } from "@/features/inbox/conversation-preview"
import { ConversationRowContent } from "@/features/inbox/conversation-row-content"
import {
  PinnedConversationList,
  pinSortableStyle,
  usePinSortable,
  type PinSortable,
} from "@/features/inbox/pinned-sort"
import { useConversationName } from "@/hooks/use-conversation-name"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { recoverSession } from "@/lib/session-navigation"
import { cn } from "@/lib/utils"

type ConversationRowProps = {
  conversation: InboxConversationData
  name: string
  showAudience: boolean
  showAssignee: boolean
  selected: boolean
  actions: ReturnType<typeof useConversationListActions>
  pinOrderVersion: string
  onMenuChange: (open: boolean) => void
  onSelect: (conversationId: string) => void
  onOpenInWindow?: (conversation: InboxConversationData, name: string) => void
  sortable?: PinSortable
}

/** 会话列表项；置顶项使用与悬停一致的底色，传入 sortable 时接入拖动与键盘排序；属性不变时跳过渲染。 */
const ConversationRow = memo(function ConversationRow({
  conversation,
  name,
  showAudience,
  showAssignee,
  selected,
  actions,
  pinOrderVersion,
  onMenuChange,
  onSelect,
  onOpenInWindow,
  sortable,
}: ConversationRowProps) {
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  if (!inboxConversationSummary(conversation)) return null
  const isInternal = isInternalInboxConversation(conversation)
  return (
    <ConversationListMenu
      conversation={conversation}
      actions={actions}
      pinOrderVersion={pinOrderVersion}
      onOpenChange={onMenuChange}
    >
        <button
          type="button"
          ref={sortable?.setNodeRef}
          style={pinSortableStyle(sortable)}
          {...sortable?.attributes}
          {...sortable?.listeners}
          data-inbox-id={conversation.id}
          data-pinned={conversation.pinned || undefined}
          aria-pressed={selected}
          aria-label={name}
          className={cn(
            "flex h-[56px] w-full min-w-0 items-start gap-2.5 px-2.5 py-2 text-left transition-colors",
            selected
              ? "bg-accent text-accent-foreground"
              : conversation.pinned
                ? "bg-muted"
                : "hover:bg-muted",
            sortable?.isDragging && "relative z-10 shadow-md",
          )}
          onClick={() => {
            // 再次点击也按进入会话处理，等待在途的手动标记完成后清除。
            if (selected && isInternal) {
              void updateConversationUnreadMark(conversation.id, {
                markedUnread: false,
              })
                .then(() => invalidate(resourceKeys.inbox()))
                .catch((error: unknown) => {
                  console.warn("清除会话未读标记失败", {
                    conversationId: conversation.id,
                    error,
                  })
                  recoverSession(error, navigate)
                })
            }
            onSelect(conversation.id)
          }}
          onDoubleClick={
            onOpenInWindow
              ? () => onOpenInWindow(conversation, name)
              : undefined
          }
        >
          <ConversationRowContent
            conversation={conversation}
            name={name}
            density="compact"
            selected={selected}
            showAssignee={showAssignee}
            showAudience={showAudience}
          />
        </button>
    </ConversationListMenu>
  )
})

/** 置顶区内可拖动与键盘排序的会话项，保存期间停用排序；属性不变时跳过渲染。 */
const SortableConversationRow = memo(function SortableConversationRow(props: ConversationRowProps) {
  const sortable = usePinSortable(props.conversation.id, props.actions.saving)
  return <ConversationRow {...props} sortable={sortable} />
})

/** 会话列表，置顶区在前且可在区内排序；showAudience 为真时名称后标明服务对象，showAssignee 为真时客户会话摘要行末显示负责人小头像，传入 onOpenInWindow 时双击会话项在独立窗口打开该会话。 */
export function InboxConversationList({
  conversations,
  showAudience,
  showAssignee,
  pinnedIds,
  pinOrderVersion,
  onMenuChange,
  onDraggingChange,
  onPinSettled,
  selectedId,
  onSelect,
  onOpenInWindow,
}: {
  conversations: InboxConversationData[]
  showAudience: boolean
  showAssignee: boolean
  pinnedIds: string[]
  pinOrderVersion: string
  onMenuChange: (open: boolean) => void
  onDraggingChange: (active: boolean) => void
  onPinSettled: (pinned: boolean) => Promise<void>
  selectedId?: string
  onSelect: (conversationId: string) => void
  onOpenInWindow?: (conversation: InboxConversationData, name: string) => void
}) {
  const conversationName = useConversationName()
  const actions = useConversationListActions(onPinSettled)
  // 列表项只接收引用稳定的回调，回调内调用本次渲染的最新实现。
  const callbacksRef = useRef({ onMenuChange, onSelect, onOpenInWindow })
  useLayoutEffect(() => {
    callbacksRef.current = { onMenuChange, onSelect, onOpenInWindow }
  })
  const callbacks = useMemo(() => ({
    onMenuChange: (open: boolean) => callbacksRef.current.onMenuChange(open),
    onSelect: (conversationId: string) => callbacksRef.current.onSelect(conversationId),
    onOpenInWindow: (conversation: InboxConversationData, name: string) =>
      callbacksRef.current.onOpenInWindow?.(conversation, name),
  }), [])

  const names = new Map(
    conversations.map((conversation) => [
      conversation.id,
      conversationName(conversation),
    ]),
  )
  const row = (conversation: InboxConversationData) => ({
    conversation,
    name: names.get(conversation.id) ?? "",
    showAudience,
    showAssignee,
    selected: selectedId === conversation.id,
    actions,
    pinOrderVersion,
    onMenuChange: callbacks.onMenuChange,
    onSelect: callbacks.onSelect,
    onOpenInWindow: onOpenInWindow ? callbacks.onOpenInWindow : undefined,
  })
  return (
    <PinnedConversationList
      conversations={conversations}
      pinnedIds={pinnedIds}
      names={names}
      pinOrderVersion={pinOrderVersion}
      actions={actions}
      onDraggingChange={onDraggingChange}
      renderPinned={(conversation) => (
        <SortableConversationRow key={conversation.id} {...row(conversation)} />
      )}
      renderRow={(conversation) => (
        <ConversationRow key={conversation.id} {...row(conversation)} />
      )}
    />
  )
}
