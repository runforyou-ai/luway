/** 会话列表统一采用用户时区下的相对时间和日历日期。 */
import { previousDayKey } from "./calendar.ts"

/** 创建用户时区下输出 YYYY-MM-DD 日期键的格式化器，用于同日、昨天和同年比较。 */
export function createDayKeyFormatter(timeZone: string) {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  })
}

/** 创建会话时间格式化器，今天和昨天之外距今不足六天时显示星期。 */
export function createConversationTimeFormatter(
  locale: string | undefined,
  timeZone: string,
  labels: { justNow: string; yesterday: string },
) {
  const relative = new Intl.RelativeTimeFormat(locale, { numeric: "always" })
  const weekday = new Intl.DateTimeFormat(locale, {
    timeZone,
    weekday: "short",
  })
  const monthDay = new Intl.DateTimeFormat(locale, {
    timeZone,
    month: "numeric",
    day: "numeric",
  })
  const fullDate = new Intl.DateTimeFormat(locale, {
    timeZone,
    year: "numeric",
    month: "numeric",
    day: "numeric",
  })
  const dayKey = createDayKeyFormatter(timeZone)

  return (value: string | null, now = new Date()) => {
    if (!value) return ""
    const date = new Date(value)
    const elapsedMs = now.getTime() - date.getTime()
    if (elapsedMs < 60_000) {
      return labels.justNow
    }
    if (elapsedMs < 3_600_000) {
      return relative.format(-Math.floor(elapsedMs / 60_000), "minute")
    }
    const day = dayKey.format(date)
    if (day === dayKey.format(now)) {
      return relative.format(-Math.floor(elapsedMs / 3_600_000), "hour")
    }
    if (day === previousDayKey(dayKey.format(now))) {
      return labels.yesterday
    }
    if (elapsedMs < 6 * 86_400_000) {
      return weekday.format(date)
    }
    if (day.slice(0, 4) === dayKey.format(now).slice(0, 4)) {
      return monthDay.format(date)
    }
    return fullDate.format(date)
  }
}
