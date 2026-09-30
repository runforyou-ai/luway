/** 客服报表共用的统计天数选项、问题类型判定与数字、占比和时长格式。 */
import { useTranslation } from "react-i18next"

import {
  ServiceIssueType,
  ServiceSessionSatisfaction,
  type ServiceIssueData,
  type ServiceIssueTypeId,
} from "@/api"

/** 可选的统计天数，第一个为默认值。 */
export const periodOptions = [30, 7, 90] as const

/** 返回问题会话在 candidates 中成立的问题类型，按 candidates 的顺序排列。 */
export function issueTypesOf(issue: ServiceIssueData, candidates: readonly ServiceIssueTypeId[]) {
  const matched: Partial<Record<ServiceIssueTypeId, boolean>> = {
    [ServiceIssueType.ServiceIssueTypeDissatisfied]:
      issue.satisfaction === ServiceSessionSatisfaction.ServiceSessionSatisfactionDissatisfied,
    [ServiceIssueType.ServiceIssueTypeAIIncorrect]: issue.aiIncorrect,
    [ServiceIssueType.ServiceIssueTypeAIMissedHandoff]: issue.aiMissedHandoff,
    [ServiceIssueType.ServiceIssueTypeAIPoorAttitude]: issue.aiPoorAttitude,
    [ServiceIssueType.ServiceIssueTypeHumanIncorrect]: issue.humanIncorrect,
    [ServiceIssueType.ServiceIssueTypeHumanPoorAttitude]: issue.humanPoorAttitude,
  }
  return candidates.filter((value) => matched[value])
}

/** 返回按当前语言格式化计数、占比与时长的方法，分母为 0 或时长没有样本时显示占位符。 */
export function useReportFormat() {
  const { t, i18n } = useTranslation("agents")
  const count = new Intl.NumberFormat(i18n.resolvedLanguage)
  const percent = new Intl.NumberFormat(i18n.resolvedLanguage, {
    style: "percent",
    maximumFractionDigits: 1,
  })
  return {
    /** 格式化计数。 */
    count: (value: number) => count.format(value),
    /** 格式化占比。 */
    rate: (part: number, total: number) =>
      total > 0 ? percent.format(part / total) : t("performance.empty"),
    /** 把秒数格式化为最多两级单位的时长。 */
    duration: (seconds: number | null) => {
      if (seconds === null) return t("performance.empty")
      const minutes = Math.floor(seconds / 60)
      const hours = Math.floor(minutes / 60)
      const days = Math.floor(hours / 24)
      if (minutes < 1) return t("performance.durations.seconds", { count: seconds })
      if (hours < 1) return t("performance.durations.minutes", { count: minutes })
      if (days < 1)
        return minutes % 60 > 0
          ? t("performance.durations.hoursMinutes", { hours, minutes: minutes % 60 })
          : t("performance.durations.hours", { count: hours })
      return hours % 24 > 0
        ? t("performance.durations.daysHours", { days, hours: hours % 24 })
        : t("performance.durations.days", { count: days })
    },
  }
}
