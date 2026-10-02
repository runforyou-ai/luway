/** 工作台与部署管理外壳共用的一级导航宽度、收起状态和顶部开关。 */
import { useCallback, useState } from "react"
import { PanelLeftCloseIcon, PanelLeftOpenIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ResizeHandle } from "@/components/resize-handle"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { readLocalPreference, writeLocalPreference } from "@/lib/local-preference"

const railMinWidth = 200
const railDefaultWidth = 220
const railMaxWidth = 300
const widthStorageKey = "app.workspace.rail-width"
const collapsedStorageKey = "app.workspace.rail-collapsed"

/** 把宽度收敛到可调范围内。 */
function clampRailWidth(width: number) {
  return Math.min(railMaxWidth, Math.max(railMinWidth, Math.round(width)))
}

/** 读取并更新本机记录的一级导航宽度和收起状态。 */
export function useWorkspaceRail() {
  const [width, setWidth] = useState(() => {
    const stored = readLocalPreference(widthStorageKey)
    return typeof stored === "number" && Number.isFinite(stored) && stored > 0
      ? clampRailWidth(stored)
      : railDefaultWidth
  })

  const [collapsed, setCollapsed] = useState(() => readLocalPreference(collapsedStorageKey) === true)

  const changeWidth = useCallback((next: number) => {
    const clamped = clampRailWidth(next)
    setWidth(clamped)
    writeLocalPreference(widthStorageKey, clamped)
  }, [])

  const toggleCollapsed = useCallback(() => {
    setCollapsed((current) => {
      const next = !current
      writeLocalPreference(collapsedStorageKey, next)
      return next
    })
  }, [])

  return { width, changeWidth, collapsed, toggleCollapsed }
}

/** 标题栏上的一级导航收起开关。 */
export function WorkspaceRailToggle({
  collapsed,
  tooltipSide,
  onToggle,
}: {
  collapsed: boolean
  tooltipSide?: "right"
  onToggle: () => void
}) {
  const { t } = useTranslation("workspace")
  const label = collapsed ? t("railOpen") : t("railClose")

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground"
          aria-label={label}
          onClick={onToggle}
        >
          {collapsed ? <PanelLeftOpenIcon /> : <PanelLeftCloseIcon />}
        </Button>
      </TooltipTrigger>
      <TooltipContent side={tooltipSide}>
        {label}
      </TooltipContent>
    </Tooltip>
  )
}

/** 一级导航右边缘的宽度拖动手柄。 */
export function WorkspaceRailResizer({
  onWidthChange,
}: {
  onWidthChange: (width: number) => void
}) {
  const { t } = useTranslation("workspace")

  return (
    <div className="relative h-full min-h-0 w-0 shrink-0">
      {/* 手柄居中于一级导航与主内容卡片之间的内缩间隙；一级导航贴着窗口左边缘，指针横坐标即为拖动后的宽度。 */}
      <ResizeHandle
        label={t("resizeNavigation")}
        className="z-30 -translate-x-px"
        onResize={onWidthChange}
      />
    </div>
  )
}
