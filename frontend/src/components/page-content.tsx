/** 主内容区的标准滚动容器。 */
import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

/** 使用统一留白承载页面主体内容；表单页使用 form 变体统一字段样式。 */
export function PageContent({
  className,
  variant = "default",
  ...props
}: ComponentProps<"div"> & { variant?: "default" | "form" }) {
  return (
    <div
      data-slot="page-content"
      className={cn(
        "app-page-gutter min-h-0 flex-1 overflow-auto py-3.5 sm:py-5",
        variant === "form" && "app-form",
        className,
      )}
      {...props}
    />
  )
}
