/** 统一头像图片、默认图案和姓名首字的展示。 */
import { useEffect, useState } from "react"
import { BotIcon, UserRoundIcon, UsersRoundIcon } from "lucide-react"

import { cn } from "@/lib/utils"

// seedTones 是按编号区分头像的色调。
const seedTones = [
  "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  "bg-rose-500/15 text-rose-700 dark:text-rose-300",
  "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  "bg-teal-500/15 text-teal-700 dark:text-teal-300",
  "bg-orange-500/15 text-orange-700 dark:text-orange-300",
  "bg-indigo-500/15 text-indigo-700 dark:text-indigo-300",
]

/** 展示圆角方形头像，图片不可用时显示默认图案或姓名首字，首字和图案按头像尺寸等比缩放；传入 seed 时按编号取固定色调。 */
export function ProfileAvatar({
  imageURL,
  name,
  fallback = "person",
  seed,
  className,
  title,
}: {
  imageURL?: string
  name?: string | null
  fallback?: "person" | "agent" | "group"
  seed?: number | null
  className?: string
  title?: string
}) {
  const [failedURL, setFailedURL] = useState<string | null>(null)
  useEffect(() => setFailedURL(null), [imageURL])
  const initial = Array.from(name?.trim() ?? "")[0]?.toLocaleUpperCase()

  return (
    <span
      aria-hidden="true"
      title={title}
      className={cn(
        "@container flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-primary/10 font-medium text-primary",
        seed != null && seedTones[seed % seedTones.length],
        className,
      )}
    >
      {imageURL && imageURL !== failedURL ? (
        <img
          key={imageURL}
          src={imageURL}
          alt=""
          className="size-full object-cover"
          draggable={false}
          onError={() => setFailedURL(imageURL)}
        />
      ) : fallback === "agent" ? (
        <BotIcon className="size-[60%]" />
      ) : fallback === "group" ? (
        <UsersRoundIcon className="size-[60%]" />
      ) : initial ? (
        <span className="text-[50cqw] leading-none">{initial}</span>
      ) : (
        <UserRoundIcon className="size-[60%]" />
      )}
    </span>
  )
}
