/** 客户会话侧栏的业务查询记录：当前客服周期内 AI 客服查询业务系统的调用、参数与结果。 */
import { BriefcaseBusinessIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { listServiceBusinessQueries } from "@/api"
import { AgentTool } from "@/components/agent-run-blocks"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource } from "@/hooks/use-resource"

/** 随会话内容变化刷新业务查询记录，按调用时间倒序展示，JSON 参数与结果格式化显示。 */
export function ServiceBusinessQueries({ conversationID }: { conversationID: string }) {
  const { t } = useTranslation("inbox")
  const { formatDateTime } = useDateTime()
  const queries = useResource(
    resourceKeys.serviceBusinessQueries(conversationID),
    () => listServiceBusinessQueries(conversationID),
  )
  if (queries.error) {
    return (
      <p className="p-3 text-xs leading-5 text-muted-foreground">
        {t("contextBusinessLoadError")}
      </p>
    )
  }
  if (!queries.data) return null
  if (queries.data.queries.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center px-6 text-center">
        <div className="mb-3 flex size-10 items-center justify-center rounded-xl border bg-muted/30 text-muted-foreground">
          <BriefcaseBusinessIcon className="size-4" />
        </div>
        <h3 className="text-sm font-medium">{t("contextBusinessEmptyTitle")}</h3>
        <p className="mt-1.5 max-w-60 text-xs leading-5 text-muted-foreground">
          {t("contextBusinessEmptyDescription")}
        </p>
      </div>
    )
  }

  /** 合法 JSON 按两格缩进展开，其余文本原样返回。 */
  function formatJSON(value: string) {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }

  return (
    <ul className="space-y-2 p-3">
      {queries.data.queries.map((query) => (
        <li key={query.id}>
          <AgentTool
            call={{
              id: query.id,
              name: query.toolName,
              arguments: formatJSON(query.arguments),
              boundArguments: query.boundArguments,
              result: query.result === null ? null : formatJSON(query.result),
              error: query.error,
              status: query.status,
            }}
            detail={[
              query.businessSystem,
              formatDateTime(query.calledAt),
              query.evidence ? t("contextBusinessEvidence") : "",
            ]
              .filter(Boolean)
              .join(" · ")}
          />
        </li>
      ))}
    </ul>
  )
}
