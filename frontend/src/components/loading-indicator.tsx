/** 延迟展示异步读取的加载提示。 */
import { useEffect, useState, type ReactNode } from "react"
import { LoaderCircleIcon } from "lucide-react"

import { cn } from "@/lib/utils"

const loadingIndicatorDelay = 200

/** 请求持续超过统一门槛后淡入加载提示。 */
export function LoadingIndicator({
  children,
  className,
}: {
  children?: ReactNode
  className?: string
}) {
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    const timer = window.setTimeout(
      () => setVisible(true),
      loadingIndicatorDelay,
    )
    return () => window.clearTimeout(timer)
  }, [])

  return (
    <div
      role={visible ? "status" : undefined}
      aria-hidden={visible ? undefined : true}
      className={cn(
        "flex items-center gap-2 text-sm text-muted-foreground opacity-0 transition-opacity duration-150",
        visible && "opacity-100",
        className,
      )}
    >
      <LoaderCircleIcon
        aria-hidden="true"
        className="size-4 shrink-0 animate-spin"
      />
      {children}
    </div>
  )
}
