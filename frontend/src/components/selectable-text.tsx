/** 精确控制普通文字的三击选区。 */
import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

/** 单击和双击使用原生行为，三击选中全部内容。 */
export function SelectableText({
  className,
  onMouseDown,
  ...props
}: ComponentProps<"span">) {
  return (
    <span
      data-slot="selectable-text"
      className={cn("select-text", className)}
      onMouseDown={(event) => {
        onMouseDown?.(event)
        if (event.defaultPrevented || event.button !== 0 || event.detail < 3) {
          return
        }

        event.preventDefault()
        const selection = window.getSelection()
        if (!selection) {
          return
        }

        const range = document.createRange()
        range.selectNodeContents(event.currentTarget)
        selection.removeAllRanges()
        selection.addRange(range)
      }}
      {...props}
    />
  )
}
