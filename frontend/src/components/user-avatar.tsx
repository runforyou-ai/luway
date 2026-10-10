/** 企业成员头像展示。 */
import type { CurrentUser } from "@/api"
import { ProfileAvatar } from "@/components/profile-avatar"

/** 展示用户头像，图片不可用时回退到姓名首字。 */
export function UserAvatar({
  user,
  className,
}: {
  user: CurrentUser
  className?: string
}) {
  return (
    <ProfileAvatar
      imageURL={user.avatarUrl}
      name={user.displayName}
      className={className}
    />
  )
}
