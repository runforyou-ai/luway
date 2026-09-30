/** 会话时间线的日期格式、日期分隔文案与相邻消息的紧凑分组判断。 */
import type { TFunction } from "i18next"

import { MessageType } from "@/api"

import { previousDayKey } from "./calendar"
import { createDayKeyFormatter } from "./conversation-time"
import { timelineSenderKey, type TimelineMessage } from "./timeline-messages"

const timelineGroupInterval = 5 * 60 * 1000

/** 时间线在用户时区下使用的全部日期格式化器。 */
export type TimelineDateFormatters = ReturnType<typeof createTimelineDateFormatters>

/** 按语言和用户时区创建时间线日期格式化器。 */
export function createTimelineDateFormatters(
  locale: string | undefined,
  timeZone: string,
) {
  return {
    clock: new Intl.DateTimeFormat(locale, {
      timeZone,
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    }),
    sessionTime: new Intl.DateTimeFormat("en-US", {
      timeZone,
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    }),
    full: new Intl.DateTimeFormat(locale, {
      timeZone,
      dateStyle: "medium",
      timeStyle: "short",
    }),
    dayKey: createDayKeyFormatter(timeZone),
    monthDay: new Intl.DateTimeFormat(locale, {
      timeZone,
      month: "long",
      day: "numeric",
    }),
    fullDate: new Intl.DateTimeFormat(locale, {
      timeZone,
      year: "numeric",
      month: "long",
      day: "numeric",
    }),
  }
}

/** 按 MM-DD HH:mm 格式显示用户时区中的消息时间。 */
export function formatMessageTime(formatter: Intl.DateTimeFormat, date: Date) {
  const parts = Object.fromEntries(
    formatter
      .formatToParts(date)
      .filter((part) => part.type !== "literal")
      .map((part) => [part.type, part.value]),
  )
  return `${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`
}

/** 按用户时区显示时间线日期分隔。 */
export function formatTimelineDayLabel(
  formatters: TimelineDateFormatters,
  date: Date,
  t: TFunction<["inbox", "common"]>,
) {
  const day = formatters.dayKey.format(date)
  const today = formatters.dayKey.format(new Date())
  if (day === today) return t("today")
  if (day === previousDayKey(today)) return t("yesterday")
  return day.slice(0, 4) === today.slice(0, 4)
    ? formatters.monthDay.format(date)
    : formatters.fullDate.format(date)
}

/** 判断相邻消息是否属于同一个紧凑展示组。 */
export function messagesShareGroup(
  previous: TimelineMessage | undefined,
  next: TimelineMessage | undefined,
  currentIdentityID: string,
  dayKey: Intl.DateTimeFormat,
) {
  if (
    !previous ||
    !next ||
    next.sessionStart ||
    previous.visibility !== next.visibility ||
    previous.type === MessageType.MessageTypeSystem ||
    previous.type === MessageType.MessageTypeAgentError ||
    previous.type === MessageType.MessageTypeAgentCancelled ||
    next.type === MessageType.MessageTypeAgentError ||
    next.type === MessageType.MessageTypeAgentCancelled ||
    next.type === MessageType.MessageTypeSystem
  ) {
    return false
  }
  if (
    timelineSenderKey(previous, currentIdentityID) !==
    timelineSenderKey(next, currentIdentityID)
  ) {
    return false
  }
  if (
    dayKey.format(new Date(previous.originatedAt)) !==
    dayKey.format(new Date(next.originatedAt))
  ) {
    return false
  }
  const interval =
    Date.parse(next.originatedAt) - Date.parse(previous.originatedAt)
  return interval >= 0 && interval <= timelineGroupInterval
}
