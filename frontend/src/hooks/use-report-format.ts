/** 报表共用的统计天数选项与数字、占比和时长格式。 */
import { useTranslation } from "react-i18next"

/** 可选的统计天数，第一个为默认值。 */
export const periodOptions = [30, 7, 90] as const

/** 返回按当前语言格式化计数、简写计数、占比与时长的方法，分母为 0 或时长没有样本时显示占位符。 */
export function useReportFormat() {
  const { t, i18n } = useTranslation("common")
  const count = new Intl.NumberFormat(i18n.resolvedLanguage)
  const compact = new Intl.NumberFormat(i18n.resolvedLanguage, { notation: "compact", maximumFractionDigits: 1 })
  const percent = new Intl.NumberFormat(i18n.resolvedLanguage, {
    style: "percent",
    maximumFractionDigits: 1,
  })
  return {
    /** 格式化计数。 */
    count: (value: number) => count.format(value),
    /** 按语言习惯简写较大的计数，如 1.2M、1.2万。 */
    compact: (value: number) => compact.format(value),
    /** 格式化占比。 */
    rate: (part: number, total: number) =>
      total > 0 ? percent.format(part / total) : t("report.empty"),
    /** 把秒数格式化为最多两级单位的时长。 */
    duration: (seconds: number | null) => {
      if (seconds === null) return t("report.empty")
      const minutes = Math.floor(seconds / 60)
      const hours = Math.floor(minutes / 60)
      const days = Math.floor(hours / 24)
      if (minutes < 1) return t("report.durations.seconds", { count: seconds })
      if (hours < 1) return t("report.durations.minutes", { count: minutes })
      if (days < 1)
        return minutes % 60 > 0
          ? t("report.durations.hoursMinutes", { hours, minutes: minutes % 60 })
          : t("report.durations.hours", { count: hours })
      return hours % 24 > 0
        ? t("report.durations.daysHours", { days, hours: hours % 24 })
        : t("report.durations.days", { count: days })
    },
  }
}
