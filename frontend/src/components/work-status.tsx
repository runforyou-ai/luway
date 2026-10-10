/** 工作状态的统一文案和语义化展示。 */
import type { ComponentProps } from "react"
import type { TFunction } from "i18next"
import { CheckIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { WorkStatus } from "@/api"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"

/** 用户菜单允许主动切换的工作状态。 */
export const selectableWorkStatuses = [
  WorkStatus.Working,
  WorkStatus.Away,
  WorkStatus.OffDuty,
] as const

/** 返回工作状态的本地化文案。 */
export function workStatusLabel(status: WorkStatus, t: TFunction<"common">) {
  switch (status) {
    case WorkStatus.Working:
      return t("workStatuses.working")
    case WorkStatus.Away:
      return t("workStatuses.away")
    case WorkStatus.OffDuty:
      return t("workStatuses.offDuty")
    default:
      console.warn("未知的工作状态", status)
      return ""
  }
}

/** 返回工作状态点使用的语义颜色。 */
function workStatusDotClass(status: WorkStatus) {
  switch (status) {
    case WorkStatus.Working:
      return "bg-success"
    case WorkStatus.Away:
      return "bg-warning"
    case WorkStatus.OffDuty:
      return "bg-muted-foreground"
    default:
      console.warn("未知的工作状态颜色", status)
      return ""
  }
}

/** 返回工作状态文字使用的语义颜色。 */
function workStatusTextClass(status: WorkStatus) {
  switch (status) {
    case WorkStatus.Working:
      return "text-success"
    case WorkStatus.Away:
      return "text-warning"
    case WorkStatus.OffDuty:
      return "text-muted-foreground"
    default:
      console.warn("未知的工作状态颜色", status)
      return ""
  }
}

/** 返回工作状态文字块使用的语义底色。 */
function workStatusTintClass(status: WorkStatus) {
  switch (status) {
    case WorkStatus.Working:
      return "bg-success/12 hover:bg-success/20"
    case WorkStatus.Away:
      return "bg-warning/12 hover:bg-warning/20"
    case WorkStatus.OffDuty:
      return "bg-muted hover:bg-muted/70"
    default:
      console.warn("未知的工作状态颜色", status)
      return ""
  }
}

/** 用小圆点显示当前工作状态。 */
export function WorkStatusDot({
  status,
  className,
  ...props
}: Omit<ComponentProps<"span">, "children"> & { status: WorkStatus }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-block size-2.5 shrink-0 rounded-full",
        workStatusDotClass(status),
        className,
      )}
      {...props}
    />
  )
}

/** 用文字徽标显示工作状态。 */
export function WorkStatusBadge({ status }: { status: WorkStatus }) {
  const { t } = useTranslation("common")

  return (
    <span className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-foreground">
      {workStatusLabel(status, t)}
    </span>
  )
}

/** 用当前工作状态文字触发的状态选择菜单，开启接待的成员在「工作中」下看到自动分配说明。 */
export function WorkStatusPicker({
  status,
  handlesServiceRequests,
  onChange,
  disabled = false,
  itemClassName,
}: {
  status: WorkStatus
  handlesServiceRequests: boolean
  onChange: (workStatus: WorkStatus) => void
  disabled?: boolean
  /** 移动端用于加高菜单项的触控区域。 */
  itemClassName?: string
}) {
  const { t } = useTranslation("workspace")
  const { t: tCommon } = useTranslation("common")

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          disabled={disabled}
          className={cn(
            "w-fit max-w-full truncate rounded-full px-2.5 text-left text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
            workStatusTextClass(status),
            workStatusTintClass(status),
          )}
          aria-label={t("workStatus")}
        >
          {workStatusLabel(status, tCommon)}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="bottom" align="start" className={handlesServiceRequests ? "w-56" : "w-36"}>
        {selectableWorkStatuses.map((workStatus) => (
          <DropdownMenuItem
            key={workStatus}
            className={itemClassName}
            onSelect={() => onChange(workStatus)}
          >
            <WorkStatusDot status={workStatus} />
            <span className="grid flex-1 gap-0.5">
              {workStatusLabel(workStatus, tCommon)}
              {handlesServiceRequests && workStatus === WorkStatus.Working ? (
                <span className="text-xs text-muted-foreground">{t("workStatusWorkingHint")}</span>
              ) : null}
            </span>
            {workStatus === status ? (
              <CheckIcon className="text-primary" />
            ) : null}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
