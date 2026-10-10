/** 桌面端与移动端会话列表项共用的头像、名称、状态、时间与摘要行。 */
import { BellOffIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  InboxPendingKind,
  isAgentInboxConversation,
  isServiceInboxConversation,
  isInternalInboxConversation,
  type InboxConversation,
} from "@/api"
import { useComposerDraft } from "@/features/inbox/state/composer-draft-context"
import { agentRunStatusLabel } from "@/features/inbox/shared/agent-run-status"
import { ConversationAssigneeAvatar, ConversationAvatar } from "@/features/inbox/shared/conversation-avatar"
import { conversationPreview, inboxConversationSummary } from "@/features/inbox/list/conversation-preview"
import { ConversationRowName } from "@/features/inbox/list/conversation-row-name"
import { ConversationUnreadBadge } from "@/features/inbox/list/conversation-unread-badge"
import { serviceAudienceOptions } from "@/features/inbox/list/inbox-query"
import { useConversationTime, useMinuteTick, useWaitingDuration } from "@/features/inbox/shared/use-conversation-time"
import { draftPreview } from "@/features/inbox/state/composer-draft-store"
import { cn } from "@/lib/utils"

/** compact 用于桌面端中栏，touch 用于移动端触屏列表。 */
const densityClasses = {
  compact: {
    avatar: "size-8",
    gap: "gap-1.5",
    name: "text-sm",
    meta: "text-[10.5px]",
    secondLine: "mt-px",
    preview: "text-xs",
    muted: "size-3",
  },
  touch: {
    avatar: "size-10",
    gap: "gap-2",
    name: "text-[15px]",
    meta: "text-xs",
    secondLine: "mt-0.5",
    preview: "text-sm",
    muted: "size-3.5",
  },
} as const

/** 渲染会话列表项内容；selected 时时间与摘要使用选中配色，showAssignee 时摘要行末显示负责人，showAudience 时名称后标明服务对象；待处理条目以等待时长代替时间，摘要前显示条目类型，另有未回应的提醒时再标 @我；未选中且有未发送草稿时摘要显示草稿。 */
export function ConversationRowContent({
  conversation,
  name,
  density,
  selected = false,
  showAssignee,
  showAudience = false,
}: {
  conversation: InboxConversation
  name: string
  density: keyof typeof densityClasses
  selected?: boolean
  showAssignee: boolean
  showAudience?: boolean
}) {
  const { t } = useTranslation("inbox")
  const formatTime = useConversationTime()
  const formatWaiting = useWaitingDuration()
  useMinuteTick()
  const draft = useComposerDraft(conversation.id)
  const summary = inboxConversationSummary(conversation)
  if (!summary) return null
  const pending = conversation.pending
  // 待领取条目标明所属队列，团队队列取团队名称。
  const pendingLabel = !pending
    ? ""
    : pending.kind === InboxPendingKind.Reply
      ? t("pendingKindReply")
      : pending.kind === InboxPendingKind.Mention
        ? t("pendingKindMention")
        : t("pendingKindQueueNamed", {
            queue: (isServiceInboxConversation(conversation) ? conversation.service.teamName : null) ?? t("queueFilterPublicQueue"),
          })
  const classes = densityClasses[density]
  const audienceLabel = showAudience && isServiceInboxConversation(conversation)
    ? serviceAudienceOptions.find((option) => option.value === conversation.service.audience)?.label
    : undefined
  const agentRunLabel = agentRunStatusLabel(
    isAgentInboxConversation(conversation)
      ? conversation.agent.agentRunStatus
      : null,
    t,
  )
  // 未选中且有草稿时显示草稿标记，只有引用目标的草稿摘要取会话末条消息。
  const showDraft = Boolean(draft) && !selected
  const preview = (showDraft && draftPreview(draft)) || conversationPreview(conversation, t)
  const formattedTime = pending ? formatWaiting(pending.since) : formatTime(summary.lastMessageAt)
  const selectedTint = selected && "text-accent-foreground/75"
  // 头像标记描边与选中、置顶行的常驻底色一致。
  const markRing = selected ? "ring-accent" : conversation.pinned ? "ring-muted" : "ring-background"
  return (
    <>
      <span className="relative shrink-0">
        <ConversationAvatar conversation={conversation} className={classes.avatar} markRingClassName={markRing} />
        <ConversationUnreadBadge conversation={conversation} />
      </span>
      <span className="min-w-0 flex-1 overflow-hidden">
        <span className={cn("grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center", classes.gap)}>
          <span className={cn("flex min-w-0 items-center", classes.gap)}>
            <ConversationRowName
              conversation={conversation}
              name={name}
              className={cn("font-medium", classes.name)}
            />
            {agentRunLabel ? (
              <span className={cn("shrink-0 text-muted-foreground", classes.meta)}>
                {agentRunLabel}
              </span>
            ) : null}
            {audienceLabel ? (
              <span className={cn("shrink-0 text-muted-foreground", classes.meta)}>
                {t(audienceLabel)}
              </span>
            ) : null}
          </span>
          {formattedTime ? (
            <time
              dateTime={(pending ? pending.since : summary.lastMessageAt) ?? undefined}
              className={cn("shrink-0 text-muted-foreground", classes.meta, selectedTint)}
            >
              {formattedTime}
            </time>
          ) : null}
        </span>
        <span className={cn("flex min-w-0 items-center", classes.gap, classes.secondLine)}>
          {pendingLabel ? (
            <span
              className={cn(
                "max-w-32 shrink-0 truncate rounded px-1 font-medium",
                classes.meta,
                pending?.kind === InboxPendingKind.Reply
                  ? "bg-destructive/10 text-destructive"
                  : pending?.kind === InboxPendingKind.Mention
                    ? "bg-primary/10 text-primary"
                    : "bg-muted text-muted-foreground",
              )}
            >
              {pendingLabel}
            </span>
          ) : null}
          {pending?.mentioned && pending.kind !== InboxPendingKind.Mention ? (
            <span className={cn("shrink-0 rounded bg-primary/10 px-1 font-medium text-primary", classes.meta)}>
              {t("pendingKindMention")}
            </span>
          ) : null}
          {showDraft ? (
            <span className={cn("shrink-0 font-medium text-destructive", classes.preview)}>
              {t("conversationDraft")}
            </span>
          ) : null}
          <span
            title={preview}
            className={cn("min-w-0 flex-1 truncate text-muted-foreground", classes.preview, selectedTint)}
          >
            {preview}
          </span>
          {showAssignee ? (
            <ConversationAssigneeAvatar conversation={conversation} className="size-4" />
          ) : null}
          {isInternalInboxConversation(conversation) && conversation.muted ? (
            <BellOffIcon
              role="img"
              className={cn("shrink-0 text-muted-foreground", classes.muted)}
              aria-label={t("conversationMuted")}
            />
          ) : null}
        </span>
      </span>
    </>
  )
}
