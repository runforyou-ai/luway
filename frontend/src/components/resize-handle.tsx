/** 分栏边缘的宽度拖动手柄。 */
import type { PointerEvent as ReactPointerEvent } from "react"

import { cn } from "@/lib/utils"

/** 结束拖动并释放指针捕获。 */
function stopResize(event: ReactPointerEvent<HTMLButtonElement>) {
  if (event.currentTarget.hasPointerCapture(event.pointerId)) {
    event.currentTarget.releasePointerCapture(event.pointerId)
  }
}

/** 竖向拖动手柄，按下后捕获指针，拖动期间持续回传指针横坐标；需放在相对定位的零宽容器内。 */
export function ResizeHandle({
  label,
  className,
  onResize,
}: {
  label: string
  className?: string
  onResize: (clientX: number) => void
}) {
  return (
    <button
      type="button"
      className={cn(
        "absolute top-0 left-0 h-full w-2 cursor-col-resize touch-none",
        className,
      )}
      aria-label={label}
      onPointerDown={(event) => {
        // 开始拖动。
        event.preventDefault()
        event.currentTarget.setPointerCapture(event.pointerId)
      }}
      onPointerMove={(event) => {
        if (!event.currentTarget.hasPointerCapture(event.pointerId)) return
        onResize(event.clientX)
      }}
      onPointerUp={stopResize}
      onPointerCancel={stopResize}
    />
  )
}
