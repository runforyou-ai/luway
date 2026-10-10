/** 在消息气泡内展示发送中和异常状态。 */
import { CircleAlertIcon, Clock3Icon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

/** 展示发送中或异常图标，并在悬停或聚焦时说明具体原因。 */
export function MessageSendState({
  state, detail,
}: {
  state: "sending" | "attention"
  detail?: string
}) {
  const { t } = useTranslation("inbox")
  const label = t("messageSendState", { context: state })
  const Icon = state === "attention" ? CircleAlertIcon : Clock3Icon

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          role="img"
          aria-label={detail ? `${label}：${detail}` : label}
          className={cn(
            "inline-flex size-3.5 shrink-0 items-center justify-center rounded-sm focus-visible:outline focus-visible:outline-2",
            state === "attention" && "text-accent-foreground",
          )}
        >
          <Icon className="size-3.5" aria-hidden="true" />
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">
        {detail || label}
      </TooltipContent>
    </Tooltip>
  )
}
