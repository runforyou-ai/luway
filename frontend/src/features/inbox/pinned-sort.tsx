/** 置顶在前的会话列表、置顶区的拖动与键盘排序，以及相对可见邻居的置顶位置命令。 */
import { useState, type ReactNode } from "react"
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core"
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { useTranslation } from "react-i18next"

import {
  ConversationPinPosition,
  type ConversationPinCommand,
  type InboxConversationData,
} from "@/api"
import type { useConversationListActions } from "@/features/inbox/conversation-list-menu"
import { pinMoveTarget } from "@/features/inbox/inbox-partitions"

/** 把置顶会话在可见置顶顺序中移到 to 位置的位置命令；位置无效或不变时返回 null。 */
export function pinMoveCommand(
  order: string[],
  conversationId: string,
  to: number,
  expectedPinOrderVersion: string,
): ConversationPinCommand | null {
  const target = pinMoveTarget(order, conversationId, to)
  if (!target) return null
  return {
    pinned: true,
    position: target.before
      ? ConversationPinPosition.ConversationPinPositionBefore
      : ConversationPinPosition.ConversationPinPositionAfter,
    neighborId: target.neighborId,
    expectedPinOrderVersion,
  }
}

/** 置顶区内一行的拖动与键盘排序状态。 */
export type PinSortable = ReturnType<typeof useSortable>

/** 接入置顶区排序；disabled 时保留位置但不可拖动。 */
export function usePinSortable(conversationId: string, disabled: boolean) {
  const { t } = useTranslation("inbox")
  return useSortable({
    id: conversationId,
    disabled,
    attributes: { roleDescription: t("pinSortRole") },
  })
}

/** 置顶行拖动中的位移与过渡样式；未接入排序时返回 undefined。 */
export function pinSortableStyle(sortable: PinSortable | undefined) {
  return sortable
    ? {
        transform: CSS.Translate.toString(sortable.transform),
        transition: sortable.transition,
      }
    : undefined
}

/** 置顶区在前且可在区内排序、其余会话在后的列表；renderPinned 按当前展示顺序渲染置顶行，renderRow 渲染其余行，separator 在两区都有会话时渲染于两区之间。 */
export function PinnedConversationList<T extends InboxConversationData>({
  conversations,
  pinnedIds,
  names,
  pinOrderVersion,
  actions,
  onDraggingChange,
  renderPinned,
  renderRow,
  separator,
}: {
  conversations: T[]
  pinnedIds: string[]
  names: Map<string, string>
  pinOrderVersion: string
  actions: ReturnType<typeof useConversationListActions>
  onDraggingChange?: (active: boolean) => void
  renderPinned: (conversation: T, order: string[], index: number) => ReactNode
  renderRow: (conversation: T) => ReactNode
  separator?: ReactNode
}) {
  const rows = new Map(conversations.map((conversation) => [conversation.id, conversation]))
  const regular = conversations.filter((conversation) => !pinnedIds.includes(conversation.id))
  return (
    <>
      <PinnedSortArea
        conversations={conversations}
        pinnedIds={pinnedIds}
        names={names}
        pinOrderVersion={pinOrderVersion}
        actions={actions}
        onDraggingChange={onDraggingChange}
      >
        {(order) =>
          order.flatMap((id, index) => {
            const conversation = rows.get(id)
            return conversation ? [renderPinned(conversation, order, index)] : []
          })
        }
      </PinnedSortArea>
      {pinnedIds.length > 0 && regular.length > 0 ? separator : null}
      {regular.map((conversation) => renderRow(conversation))}
    </>
  )
}

/** 置顶区排序容器：放下后先按临时顺序展示，置顶区权威顺序重读完成或保存失败后撤销，children 按当前展示顺序渲染置顶项。 */
function PinnedSortArea({
  conversations,
  pinnedIds,
  names,
  pinOrderVersion,
  actions,
  onDraggingChange,
  children,
}: {
  conversations: InboxConversationData[]
  pinnedIds: string[]
  names: Map<string, string>
  pinOrderVersion: string
  actions: ReturnType<typeof useConversationListActions>
  onDraggingChange?: (active: boolean) => void
  children: (order: string[]) => ReactNode
}) {
  const { t } = useTranslation("inbox")
  const [pendingOrder, setPendingOrder] = useState<string[] | null>(null)
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
      keyboardCodes: { start: ["Space"], cancel: ["Escape"], end: ["Space", "Enter"] },
    }),
  )
  const order =
    pendingOrder?.length === pinnedIds.length && pendingOrder.every((id) => pinnedIds.includes(id))
      ? pendingOrder
      : pinnedIds
  const announce = (key: "pinSortPicked" | "pinSortMoved" | "pinSortDropped" | "pinSortCancelled", id: string | number, overId?: string | number) =>
    t(key, { name: names.get(String(id)) ?? "", position: order.indexOf(String(overId ?? id)) + 1, total: order.length })

  /** 把放下位置转成相对可见邻居的位置命令并保存。 */
  function drop({ active, over }: DragEndEvent) {
    onDraggingChange?.(false)
    const conversation = conversations.find((row) => row.id === String(active.id))
    const to = over ? order.indexOf(String(over.id)) : -1
    const command = pinMoveCommand(order, String(active.id), to, pinOrderVersion)
    if (!conversation || !command) return
    setPendingOrder(arrayMove(order, order.indexOf(conversation.id), to))
    void actions.updatePin(conversation, command).finally(() => setPendingOrder(null))
  }

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      modifiers={[({ transform }) => ({ ...transform, x: 0 })]}
      accessibility={{
        // 读屏说明与播报节点挂到 body，列表容器只含会话行。
        container: document.body,
        screenReaderInstructions: { draggable: t("pinSortInstructions") },
        announcements: {
          onDragStart: ({ active }) => announce("pinSortPicked", active.id),
          onDragOver: ({ active, over }) => over ? announce("pinSortMoved", active.id, over.id) : undefined,
          onDragEnd: ({ active, over }) => announce("pinSortDropped", active.id, over?.id),
          onDragCancel: ({ active }) => announce("pinSortCancelled", active.id),
        },
      }}
      onDragStart={() => onDraggingChange?.(true)}
      onDragEnd={drop}
      onDragCancel={() => onDraggingChange?.(false)}
    >
      <SortableContext items={order} strategy={verticalListSortingStrategy}>
        {children(order)}
      </SortableContext>
    </DndContext>
  )
}
