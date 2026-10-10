/** 数组表单行的上移、下移与删除图标按钮。 */
import type { ReactNode } from "react"
import { ArrowDownIcon, ArrowUpIcon, XIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/** 按行位置禁用首行上移与末行下移；给出 remove 时在末尾显示删除按钮，未给出图标时用叉号。 */
export function ArrayRowActions({
  index,
  count,
  onMove,
  moveUpLabel,
  moveDownLabel,
  remove,
  size = "icon",
  className,
}: {
  index: number
  count: number
  onMove: (from: number, to: number) => void
  moveUpLabel: string
  moveDownLabel: string
  remove?: { label: string; onRemove: () => void; disabled?: boolean; icon?: ReactNode }
  size?: "icon" | "icon-sm"
  className?: string
}) {
  return (
    <div className={cn("flex items-center", className)}>
      <Button
        type="button"
        variant="ghost"
        size={size}
        disabled={index === 0}
        aria-label={moveUpLabel}
        title={moveUpLabel}
        onClick={() => onMove(index, index - 1)}
      >
        <ArrowUpIcon />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size={size}
        disabled={index === count - 1}
        aria-label={moveDownLabel}
        title={moveDownLabel}
        onClick={() => onMove(index, index + 1)}
      >
        <ArrowDownIcon />
      </Button>
      {remove ? (
        <Button
          type="button"
          variant="ghost"
          size={size}
          disabled={remove.disabled}
          aria-label={remove.label}
          title={remove.label}
          onClick={remove.onRemove}
        >
          {remove.icon ?? <XIcon />}
        </Button>
      ) : null}
    </div>
  )
}
