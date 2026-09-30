/** 助理头像右下角的在线状态标记。 */
import { PauseIcon } from "lucide-react"

import { AssistantPresence } from "@/api"
import { cn } from "@/lib/utils"

/** 在线显示绿点，离线或未绑定电脑显示灰点，暂停显示暂停标记，已停用或未知状态不显示；紧凑尺寸用于小头像，暂停只显示色点。 */
export function AssistantPresenceMark({
  presence,
  compact = false,
  className,
}: {
  presence: AssistantPresence | null | undefined
  compact?: boolean
  className?: string
}) {
  const dot = cn("inline-block shrink-0 rounded-full", compact ? "size-2" : "size-2.5")
  switch (presence) {
    case AssistantPresence.AssistantPresenceOnline:
      return <span aria-hidden="true" className={cn(dot, "bg-success", className)} />
    case AssistantPresence.AssistantPresenceOffline:
    case AssistantPresence.AssistantPresenceUnbound:
      return <span aria-hidden="true" className={cn(dot, "bg-muted-foreground", className)} />
    case AssistantPresence.AssistantPresencePaused:
      return compact ? (
        <span aria-hidden="true" className={cn(dot, "bg-warning", className)} />
      ) : (
        <span
          aria-hidden="true"
          className={cn("flex size-3.5 shrink-0 items-center justify-center rounded-full bg-warning text-background", className)}
        >
          <PauseIcon className="size-2 fill-current" strokeWidth={3} />
        </span>
      )
    default:
      return null
  }
}
