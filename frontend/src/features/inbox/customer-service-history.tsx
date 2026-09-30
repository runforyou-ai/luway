/** 客服侧栏客户资料中的历史咨询：同一客户各渠道已关闭周期的小结，每个周期一行。 */
import { useTranslation } from "react-i18next"

import { useDateTime } from "@/hooks/use-date-time"

import {
  ServiceSessionSummaryEditButton,
  ServiceSessionSummaryText,
  useServiceSummaries,
} from "./service-session-summary"

/** 按关闭时间从新到旧列出客户历史周期的小结；鼠标设备在悬停或聚焦时显示修改入口，触屏设备常驻显示。 */
export function CustomerServiceHistory({ conversationID }: { conversationID: string }) {
  const { t } = useTranslation("inbox")
  const { formatDateTime } = useDateTime()
  const summaries = useServiceSummaries(conversationID)
  if (summaries.error) {
    return <p className="mt-5 text-xs leading-5 text-muted-foreground">{t("summaryLoadError")}</p>
  }
  const sessions = summaries.data?.sessions ?? []
  if (sessions.length === 0) return null
  return (
    <section className="mt-5 space-y-2">
      <h3 className="text-xs font-medium text-muted-foreground">{t("summaryHistory")}</h3>
      <ul className="space-y-1">
        {sessions.map((summary) => (
          <li
            key={summary.serviceSessionId}
            className="group flex items-start gap-1 rounded-md px-2 py-1.5 hover:bg-muted/50 focus-within:bg-muted/50"
          >
            <div className="min-w-0 flex-1">
              <ServiceSessionSummaryText
                summary={summary}
                meta={[formatDateTime(summary.closedAt), summary.channelName ?? ""]}
              />
            </div>
            <ServiceSessionSummaryEditButton
              conversationID={conversationID}
              summary={summary}
              className="size-7 shrink-0 text-muted-foreground pointer-fine:opacity-0 pointer-fine:group-hover:opacity-100 pointer-fine:group-focus-within:opacity-100"
            />
          </li>
        ))}
      </ul>
    </section>
  )
}
