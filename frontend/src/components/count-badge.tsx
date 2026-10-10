/** 导航与页签共用的数量徽标。 */
import { cn } from "@/lib/utils"

/** 数量徽标的配色：alert 为未读提醒，muted 为静音未读，neutral 为待办数量。 */
export type CountBadgeTone = "alert" | "muted" | "neutral"

/** 按配色显示数量，超过 99 显示 99+。 */
export function CountBadge({
  count,
  label,
  tone = "alert",
  className,
}: {
  count: number
  label?: string
  tone?: CountBadgeTone
  className?: string
}) {
  return (
    <span
      aria-label={label}
      className={cn(
        "flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full px-1 text-[10px] leading-none font-semibold tabular-nums",
        tone === "alert" && "bg-destructive text-destructive-foreground",
        tone === "muted" && "bg-muted text-muted-foreground",
        tone === "neutral" && "bg-foreground/10 text-foreground",
        className,
      )}
    >
      {count > 99 ? "99+" : count}
    </span>
  )
}
