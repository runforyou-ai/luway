/** 表单分组校验提示槽。 */
import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

/** 保留一行高度并显示表单分组校验提示。 */
export function FormValidationMessage({
  message,
  className,
  ...props
}: Omit<ComponentProps<"p">, "children"> & { message?: string }) {
  return (
    <p
      role="alert"
      aria-atomic="true"
      className={cn("min-h-5 text-sm text-destructive", className)}
      {...props}
    >
      {message}
    </p>
  )
}
