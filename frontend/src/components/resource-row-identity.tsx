/** 数据列表首列的主信息：头像或图标、名称与次要信息、补充说明。 */
import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"

import { ProfileAvatar } from "@/components/profile-avatar"

type RowAvatar = {
  imageURL?: string
  name?: string | null
  fallback?: "person" | "agent" | "group"
  seed?: number | null
}

/** 渲染行首头像（可带右下角状态标记）、圆形图标或传入的行首元素，主行为名称加 `·` 分隔的次要信息，第二行为补充说明；文字说明按单行截断，传入元素时由元素自行控制截断。 */
export function ResourceRowIdentity({
  avatar,
  leading,
  icon: Icon,
  mark,
  name,
  secondary,
  badge,
  description,
}: {
  avatar?: RowAvatar
  leading?: ReactNode
  icon?: LucideIcon
  mark?: ReactNode
  name: ReactNode
  secondary?: ReactNode
  badge?: ReactNode
  description?: ReactNode
}) {
  const title = (
    <span className="min-w-0 truncate">
      <span className="font-medium">{name}</span>
      {secondary ? (
        <>
          <span aria-hidden="true" className="mx-1.5 text-muted-foreground">·</span>
          <span className="text-muted-foreground">{secondary}</span>
        </>
      ) : null}
    </span>
  )

  return (
    <div className="flex min-w-0 items-center gap-3">
      {leading ? (
        leading
      ) : Icon ? (
        <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
          <Icon className="size-4.5" aria-hidden="true" />
        </span>
      ) : (
        <span className="relative size-9 shrink-0">
          <ProfileAvatar {...avatar} className="size-full" />
          {mark ? (
            <span className="absolute -right-0.5 -bottom-0.5 flex rounded-full ring-2 ring-background empty:hidden">
              {mark}
            </span>
          ) : null}
        </span>
      )}
      <span className="grid min-w-0 gap-0.5 leading-tight">
        {badge ? (
          <span className="flex min-w-0 items-center gap-2">
            {title}
            {badge}
          </span>
        ) : (
          title
        )}
        {!description ? null : typeof description === "string" ? (
          <span className="truncate text-xs text-muted-foreground">{description}</span>
        ) : (
          <span className="flex min-w-0 items-center text-xs text-muted-foreground">
            {description}
          </span>
        )}
      </span>
    </div>
  )
}
