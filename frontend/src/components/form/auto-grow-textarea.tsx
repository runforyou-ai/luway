/** 默认一行高、随内容自动增高的多行输入框。 */
import { useLayoutEffect, useRef, type ComponentProps } from "react"

import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"

/** 渲染一行起步的受控多行输入框，取值变化时按内容自动增高。 */
export function AutoGrowTextarea({
  className,
  ref,
  ...props
}: ComponentProps<typeof Textarea>) {
  const inputRef = useRef<HTMLTextAreaElement | null>(null)

  useLayoutEffect(() => {
    // 按内容高度加上下边框调整高度，超过 184px 后滚动。
    const input = inputRef.current
    if (!input) return
    input.style.height = "auto"
    input.style.height = `${Math.min(input.scrollHeight + input.offsetHeight - input.clientHeight, 184)}px`
  }, [props.value])

  return (
    <Textarea
      {...props}
      ref={(input) => {
        inputRef.current = input
        if (typeof ref === "function") ref(input)
        else if (ref) ref.current = input
      }}
      rows={1}
      className={cn("min-h-0 max-h-[184px] resize-none leading-6", className)}
    />
  )
}
