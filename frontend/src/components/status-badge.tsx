/** 通用状态徽标。 */
import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

/** 按语义显示成功、警示、失败或中性状态。 */
export function StatusBadge({
  variant,
  showDot = true,
  className,
  children,
  ...props
}: ComponentProps<"span"> & {
  variant: "success" | "warning" | "destructive" | "muted"
  showDot?: boolean
}) {
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center whitespace-nowrap font-medium",
        variant === "success" &&
          "gap-1.5 rounded-full bg-success/15 px-2 py-0.5 text-xs text-success",
        variant === "warning" &&
          "gap-1.5 rounded-full bg-warning/15 px-2 py-0.5 text-xs text-warning",
        variant === "destructive" &&
          "gap-1.5 rounded-full bg-destructive/15 px-2 py-0.5 text-xs text-destructive",
        variant === "muted" &&
          "rounded-sm bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground",
        className,
      )}
      {...props}
    >
      {variant !== "muted" && showDot ? (
        <span
          aria-hidden="true"
          className={cn(
            "size-1.5 rounded-full",
            variant === "success" ? "bg-success" : variant === "warning" ? "bg-warning" : "bg-destructive",
          )}
        />
      ) : null}
      {children}
    </span>
  )
}
