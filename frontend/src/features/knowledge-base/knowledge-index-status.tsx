/** 知识来源索引状态与失败原因的上方悬浮提示。 */
import { useTranslation } from "react-i18next"
import type { KnowledgeIndexStatusId } from "@/api"
import { StatusBadge } from "@/components/status-badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/** 在文档和问答列表中显示索引状态，悬停或聚焦失败状态时展示原因。 */
export function KnowledgeIndexStatus({ status, failureMessage }: { status: KnowledgeIndexStatusId; failureMessage: string }) {
  const { t } = useTranslation("knowledgeBase")
  const showFailure = status === "failed" && Boolean(failureMessage)
  const badge = (
    <StatusBadge
      variant={status === "failed" ? "destructive" : "muted"}
      showDot={false}
      tabIndex={showFailure ? 0 : undefined}
      className={`rounded-sm px-1.5 py-0.5 text-[10px]${showFailure ? " cursor-help outline-none focus-visible:ring-2 focus-visible:ring-ring" : ""}`}
    >
      {t(`indexStatus.${status}`)}
    </StatusBadge>
  )
  if (!showFailure) return badge
  return (
    <Tooltip>
      <TooltipTrigger asChild>{badge}</TooltipTrigger>
      <TooltipContent side="top" sideOffset={6} className="max-w-sm whitespace-pre-wrap break-words">
        {failureMessage}
      </TooltipContent>
    </Tooltip>
  )
}
