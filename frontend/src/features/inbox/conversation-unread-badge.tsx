/** 会话头像上的未读数量、提及和手动未读角标。 */
import { useTranslation } from "react-i18next"

import type { InboxConversationData } from "@/api"
import { cn } from "@/lib/utils"

/** 展示会话未读数量和提及标记，静音会话使用弱化颜色。 */
export function ConversationUnreadBadge({
  conversation,
}: {
  conversation: InboxConversationData
}) {
  const { t } = useTranslation("inbox")
  if (conversation.unreadCount === 0 && !conversation.markedUnread) return null

  return (
    <span
      className={cn(
        "absolute rounded-full ring-2 ring-background",
        conversation.unreadCount > 0
          ? "-top-1 -right-1.5 flex h-4 min-w-4 items-center justify-center gap-0.5 px-1 text-[10px] leading-none font-semibold tabular-nums"
          : "-top-0.5 -right-0.5 size-2.5",
        conversation.muted
          ? "bg-muted text-muted-foreground"
          : "bg-destructive text-destructive-foreground",
      )}
    >
      {conversation.unreadCount > 0 ? (
        <>
          {conversation.mentionedUnreadCount > 0 ? "@" : null}
          {conversation.unreadCount > 99 ? "99+" : conversation.unreadCount}
        </>
      ) : (
        <span className="sr-only">{t("conversationMarkedUnread")}</span>
      )}
    </span>
  )
}
