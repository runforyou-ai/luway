/** 时间线周期关闭事件下的小结：默认收起，展开后显示小结正文与标注。 */
import { useState } from "react"
import { ChevronDownIcon, ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  ServiceSessionSummaryEditButton,
  ServiceSessionSummaryText,
  useServiceSummaries,
} from "./service-session-summary"

/** 按周期编号取客户周期小结，存在小结状态时显示展开入口。 */
export function TimelineSessionSummary({
  conversationID,
  serviceSessionID,
}: {
  conversationID: string
  serviceSessionID: string
}) {
  const { t } = useTranslation("inbox")
  const [expanded, setExpanded] = useState(false)
  const summaries = useServiceSummaries(conversationID)
  const summary = summaries.data?.sessions.find((item) => item.serviceSessionId === serviceSessionID)
  if (!summary?.status) return null
  const Icon = expanded ? ChevronDownIcon : ChevronRightIcon
  return (
    <>
      <button
        type="button"
        className="inline-flex items-center gap-0.5 rounded-sm text-xs text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        <Icon className="size-3.5" />
        {t("summaryToggle")}
      </button>
      {expanded ? (
        <div className="mt-1 flex w-full max-w-lg items-start gap-2 rounded-lg border bg-background px-3 py-2 text-left">
          <div className="min-w-0 flex-1">
            <ServiceSessionSummaryText summary={summary} />
          </div>
          <ServiceSessionSummaryEditButton conversationID={conversationID} summary={summary} className="-mr-1 size-7 shrink-0 text-muted-foreground" />
        </div>
      ) : null}
    </>
  )
}
