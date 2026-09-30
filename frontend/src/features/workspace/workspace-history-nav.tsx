/** 工作台内的前进后退状态和入口。 */
import { useEffect, useState } from "react"
import { ChevronLeftIcon, ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { NavigationType, useLocation, useNavigate, useNavigationType } from "react-router"

import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

/** 跟踪工作台挂载后的地址栈，得出前进后退的可用状态；由常驻外壳持有，按钮收起后记录不丢失。 */
export function useWorkspaceHistory() {
  const location = useLocation()
  const navigationType = useNavigationType()
  const navigate = useNavigate()
  const [stack, setStack] = useState(() => ({
    keys: [location.key],
    index: 0,
  }))

  useEffect(() => {
    setStack((current) => {
      const visited = current.keys.indexOf(location.key)
      if (navigationType === NavigationType.Pop) {
        // 退到工作台挂载之前的地址时重新开始记录。
        return visited === -1
          ? { keys: [location.key], index: 0 }
          : { keys: current.keys, index: visited }
      }
      if (navigationType === NavigationType.Replace) {
        const keys = [...current.keys]
        keys[current.index] = location.key
        return { keys, index: current.index }
      }
      const keys = [...current.keys.slice(0, current.index + 1), location.key]
      return { keys, index: keys.length - 1 }
    })
  }, [location.key, navigationType])

  return {
    canGoBack: stack.index > 0,
    canGoForward: stack.index < stack.keys.length - 1,
    goBack: () => navigate(-1),
    goForward: () => navigate(1),
  }
}

/** 标题栏上的前进后退按钮。 */
export function WorkspaceHistoryNav({
  history,
}: {
  history: ReturnType<typeof useWorkspaceHistory>
}) {
  const { t } = useTranslation("workspace")
  const { canGoBack, canGoForward, goBack, goForward } = history

  return (
    <div className="flex items-center">
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground"
            aria-label={t("historyBack")}
            disabled={!canGoBack}
            onClick={goBack}
          >
            <ChevronLeftIcon />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("historyBack")}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground"
            aria-label={t("historyForward")}
            disabled={!canGoForward}
            onClick={goForward}
          >
            <ChevronRightIcon />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("historyForward")}</TooltipContent>
      </Tooltip>
    </div>
  )
}
