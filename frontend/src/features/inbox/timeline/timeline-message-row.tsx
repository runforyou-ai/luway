/** 时间线中的单条消息行：日期分隔、客服处理周期分隔，以及系统事件、AI 员工操作卡片或消息气泡。 */
import { memo } from "react"
import { useTranslation } from "react-i18next"

import { ConversationSystemEventType, MessageType, ServiceSessionStatus } from "@/api"

import {
  formatMessageTime,
  formatTimelineDayLabel,
  messagesShareGroup,
} from "./timeline-grouping"
import {
  TimelineMessageBubble,
  type TimelineMessageBubbleContext,
} from "./timeline-message-bubble"
import type { TimelineMessage } from "./timeline-messages"
import { TimelineSessionSummary } from "./timeline-session-summary"
import { formatSystemEvent } from "./timeline-system-event"
import { TimelineToolDecisionCard } from "./timeline-tool-decision-card"

/** 按相邻消息决定分隔与分组，展示一条时间线消息；summaryEvent 为 true 的周期关闭事件附带该周期小结，unreadStart 为 true 时在消息前标出新消息起点；属性不变时跳过渲染。 */
export const TimelineMessageRow = memo(function TimelineMessageRow({
  message,
  previous,
  next,
  summaryEvent = false,
  unreadStart = false,
  ...context
}: TimelineMessageBubbleContext & {
  message: TimelineMessage
  previous: TimelineMessage | undefined
  next: TimelineMessage | undefined
  summaryEvent?: boolean
  unreadStart?: boolean
}) {
  const { t } = useTranslation(["inbox", "common"])
  const { formatters } = context
  const currentIdentityID = context.currentUser.identityId
  const date = new Date(message.originatedAt)
  const day = formatters.dayKey.format(date)
  const startsDay =
    !previous ||
    formatters.dayKey.format(
      new Date(previous.originatedAt),
    ) !== day
  const systemEvent = message.systemEvent
  const systemEventText = systemEvent
    ? formatSystemEvent(systemEvent, currentIdentityID, t)
    : null
  // AI 员工提交的操作以卡片展示，操作内容缺失时退回系统事件文案。
  const toolDecision =
    systemEvent?.type === ConversationSystemEventType.ConversationSystemEventAgentToolCallPending
      ? systemEvent.toolCall
      : null

  return (
    <div>
      {startsDay ? (
        <div className="my-3 flex items-center gap-2.5 text-xs font-medium text-muted-foreground">
          <span className="h-px flex-1 bg-border/50" />
          <time dateTime={day}>{formatTimelineDayLabel(formatters, date, t)}</time>
          <span className="h-px flex-1 bg-border/50" />
        </div>
      ) : null}
      {message.sessionStart ? (
        <div className="my-3 text-center text-xs font-semibold text-muted-foreground">
          <span>
            {t("sessionBoundary", {
              sequence: message.sessionStart.sequence,
              time: formatMessageTime(
                formatters.sessionTime,
                new Date(message.sessionStart.startedAt),
              ),
            })}{" "}
            ·{" "}
            {message.sessionStart.status ===
            ServiceSessionStatus.Closed
              ? t("sessionBoundaryClosed")
              : t("sessionBoundaryOngoing")}
          </span>
        </div>
      ) : null}
      {unreadStart ? (
        <div data-unread-start="" className="my-3 flex items-center gap-2.5 text-xs font-medium text-destructive">
          <span className="h-px flex-1 bg-destructive/40" />
          <span>{t("messagesUnreadStart")}</span>
          <span className="h-px flex-1 bg-destructive/40" />
        </div>
      ) : null}
      {message.type === MessageType.System && toolDecision ? (
        <div
          data-message-id={message.local ? undefined : message.id}
          tabIndex={-1}
          className="my-3 flex justify-center px-4"
        >
          <TimelineToolDecisionCard decision={toolDecision} conversationID={context.conversationID} />
        </div>
      ) : message.type === MessageType.System &&
      systemEventText ? (
        <div
          data-message-id={message.local ? undefined : message.id}
          tabIndex={-1}
          className="my-3 flex flex-col items-center gap-1 px-10 text-center text-xs text-muted-foreground"
        >
          <span title={formatters.full.format(date)}>
            {systemEventText}
          </span>
          {systemEvent?.reasonText ? (
            <span className="max-w-full break-words">
              {systemEvent.reasonText}
            </span>
          ) : null}
          {systemEvent?.ratingComment ? (
            <span className="max-w-full whitespace-pre-wrap break-words">
              {systemEvent.ratingComment}
            </span>
          ) : null}
          {summaryEvent && systemEvent?.serviceSessionId ? (
            <TimelineSessionSummary
              conversationID={context.conversationID}
              serviceSessionID={systemEvent.serviceSessionId}
            />
          ) : null}
        </div>
      ) : (
        <TimelineMessageBubble
          {...context}
          message={message}
          date={date}
          spaced={Boolean(previous)}
          startsGroup={!messagesShareGroup(previous, message, currentIdentityID, formatters.dayKey)}
          endsGroup={!messagesShareGroup(message, next, currentIdentityID, formatters.dayKey)}
        />
      )}
    </div>
  )
})
