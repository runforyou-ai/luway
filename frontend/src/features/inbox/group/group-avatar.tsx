/** 群聊图片展示。 */
import { ProfileAvatar } from "@/components/profile-avatar"
import { cn } from "@/lib/utils"

/** 展示自定义群图片，并在缺失或加载失败时回退到默认群组图标。 */
export function GroupAvatar({
  imageURL,
  className,
}: {
  imageURL?: string
  className?: string
}) {
  return (
    <ProfileAvatar
      imageURL={imageURL}
      fallback="group"
      className={cn("size-full", className)}
    />
  )
}
