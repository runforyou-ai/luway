/** 会话列表项的阅读状态、静音、置顶与归档操作及右键与长按菜单。 */
import { useMemo, useRef, type ReactElement } from "react"
import { useTranslation } from "react-i18next"

import {
  isInternalInboxConversation,
  markConversationRead,
  updateConversationNotificationSettings,
  updateConversationPin,
  updateConversationUnreadMark,
  type ConversationPinCommand,
  type InboxConversation,
} from "@/api"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { useConversationArchive } from "@/features/inbox/list/use-conversation-archive"
import { resourceKeys } from "@/hooks/resource-keys"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 更新会话阅读状态失败时的日志文案与提示词条。 */
const readStateFailure = {
  log: "更新会话阅读状态失败",
  message: "conversationReadStateError",
} as const

/** 串行保存列表中的会话个人设置，成功后重新读取统一收件箱；onPinSettled 在置顶写入成功后按目标分区优先的顺序重读列表。 */
export function useConversationListActions(onPinSettled?: (pinned: boolean) => Promise<void>) {
  const { t } = useTranslation("inbox")
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const settingsSave = useImmediateSave()
  const archive = useConversationArchive()

  /** 执行一项个人设置保存，失败时按错误原因提示。 */
  async function save(
    conversation: InboxConversation,
    update: () => Promise<unknown>,
    failure: {
      log: string
      message:
        | "conversationReadStateError"
        | "conversationMuteError"
        | "conversationPinError"
      reload?: boolean
    },
  ) {
    const request = settingsSave.begin()
    if (request === null) return
    try {
      await update()
      await invalidate(resourceKeys.inbox())
    } catch (error) {
      if (!settingsSave.isCurrent(request)) return
      console.warn(failure.log, { conversationId: conversation.id, error })
      // 置顶与归档写入失败后重读列表，恢复权威的置顶、归档状态与置顶顺序版本。
      if (failure.reload) void invalidate(resourceKeys.inbox())
      reportError(error, { fallback: t(failure.message) })
    } finally {
      settingsSave.finish(request)
    }
  }

  const latestActions = {
    // 有消息时把真实已读推进到列表最后一条消息并清除手动未读。
    markRead: (conversation: InboxConversation) =>
      save(
        conversation,
        () =>
          conversation.lastMessageId
            ? markConversationRead(conversation.id, {
                lastReadMessageId: conversation.lastMessageId,
                clearUnreadMark: true,
              })
            : updateConversationUnreadMark(conversation.id, {
                markedUnread: false,
              }),
        readStateFailure,
      ),
    markUnread: (conversation: InboxConversation) =>
      save(
        conversation,
        () =>
          updateConversationUnreadMark(conversation.id, { markedUnread: true }),
        readStateFailure,
      ),
    // 静音状态同时显示在会话头与群资料面板，一并刷新会话摘要与群资料。
    toggleMuted: (conversation: InboxConversation) =>
      save(
        conversation,
        async () => {
          await updateConversationNotificationSettings(conversation.id, {
            muted: !conversation.muted,
          })
          await Promise.all([
            invalidate(resourceKeys.conversationSummary(conversation.id)),
            invalidate(resourceKeys.groupConversation(conversation.id)),
          ])
        },
        { log: "更新会话静音设置失败", message: "conversationMuteError" },
      ),
    // 置顶、取消置顶与按邻居移动共用同一条写入，顺序版本冲突由服务端拒绝。
    updatePin: (conversation: InboxConversation, command: ConversationPinCommand) =>
      save(conversation, async () => {
        await updateConversationPin(conversation.id, command)
        await onPinSettled?.(command.pinned)
      }, {
        log: "更新会话置顶失败",
        message: "conversationPinError",
        reload: true,
      }),
    // 归档按会话单独保存并提示结果；归档同时取消置顶，置顶区与普通区一并重读。
    setArchived: async (conversation: InboxConversation, archived: boolean) => {
      if (await archive.save(conversation.id, archived, conversation.pinned) && conversation.pinned) await onPinSettled?.(false)
    },
  }
  // 列表项只接收引用稳定的操作入口，入口内调用本次渲染的最新实现。
  const actionsRef = useRef(latestActions)
  actionsRef.current = latestActions
  // 归档保存期间同样停用列表菜单与置顶排序。
  const saving = settingsSave.saving || archive.saving
  return useMemo(() => ({
    saving,
    markRead: (conversation: InboxConversation) => actionsRef.current.markRead(conversation),
    markUnread: (conversation: InboxConversation) => actionsRef.current.markUnread(conversation),
    toggleMuted: (conversation: InboxConversation) => actionsRef.current.toggleMuted(conversation),
    updatePin: (conversation: InboxConversation, command: ConversationPinCommand) =>
      actionsRef.current.updatePin(conversation, command),
    toggleArchived: (conversation: InboxConversation) =>
      actionsRef.current.setArchived(conversation, conversation.archivedAt === null),
  }), [saving])
}

/** 为会话列表项提供阅读状态、静音、置顶与归档菜单，右键或长按触发，没有可用操作时不打开；群聊、单聊与 AI 聊天提供归档；传入 pinOrderVersion 时提供置顶操作，置顶项再传入 pinMoves 时提供移动与排序入口。 */
export function ConversationListMenu({
  conversation,
  actions,
  itemClassName,
  pinOrderVersion,
  pinMoves,
  onOpenChange,
  children,
}: {
  conversation: InboxConversation
  actions: ReturnType<typeof useConversationListActions>
  itemClassName?: string
  pinOrderVersion?: string
  pinMoves?: {
    up: ConversationPinCommand | null
    down: ConversationPinCommand | null
    sort: () => void
  }
  onOpenChange?: (open: boolean) => void
  children: ReactElement
}) {
  const { t } = useTranslation("inbox")
  const hasUnread = conversation.unreadCount > 0 || conversation.markedUnread
  const internal = isInternalInboxConversation(conversation)

  return (
    <ContextMenu onOpenChange={onOpenChange}>
      <ContextMenuTrigger asChild disabled={!internal && !hasUnread && pinOrderVersion === undefined}>
        {children}
      </ContextMenuTrigger>
      <ContextMenuContent>
        {hasUnread ? (
          <ContextMenuItem
            className={itemClassName}
            disabled={actions.saving}
            onSelect={() => void actions.markRead(conversation)}
          >
            {t("conversationMarkRead")}
          </ContextMenuItem>
        ) : null}
        {internal && !conversation.markedUnread ? (
          <ContextMenuItem
            className={itemClassName}
            disabled={actions.saving}
            onSelect={() => void actions.markUnread(conversation)}
          >
            {t("conversationMarkUnread")}
          </ContextMenuItem>
        ) : null}
        {internal ? (
          <ContextMenuItem
            className={itemClassName}
            disabled={actions.saving}
            onSelect={() => void actions.toggleMuted(conversation)}
          >
            {t(conversation.muted ? "conversationUnmute" : "conversationMute")}
          </ContextMenuItem>
        ) : null}
        {pinOrderVersion !== undefined ? (
          <ContextMenuItem
            className={itemClassName}
            disabled={actions.saving}
            onSelect={() =>
              void actions.updatePin(conversation, {
                pinned: !conversation.pinned,
                expectedPinOrderVersion: pinOrderVersion,
              })
            }
          >
            {t(conversation.pinned ? "conversationUnpin" : "conversationPin")}
          </ContextMenuItem>
        ) : null}
        {pinOrderVersion !== undefined && conversation.pinned && pinMoves ? (
          <>
            {([["pinMoveUp", pinMoves.up], ["pinMoveDown", pinMoves.down]] as const).map(([label, command]) => (
              <ContextMenuItem
                key={label}
                className={itemClassName}
                disabled={actions.saving || !command}
                onSelect={() => {
                  if (command) void actions.updatePin(conversation, command)
                }}
              >
                {t(label)}
              </ContextMenuItem>
            ))}
            <ContextMenuItem className={itemClassName} onSelect={pinMoves.sort}>
              {t("pinSortStart")}
            </ContextMenuItem>
          </>
        ) : null}
        {internal ? (
          <ContextMenuItem
            className={itemClassName}
            disabled={actions.saving}
            onSelect={() => void actions.toggleArchived(conversation)}
          >
            {t(conversation.archivedAt === null ? "conversationArchive" : "conversationUnarchive")}
          </ContextMenuItem>
        ) : null}
      </ContextMenuContent>
    </ContextMenu>
  )
}
