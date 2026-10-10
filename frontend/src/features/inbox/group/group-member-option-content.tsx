/** 群成员候选行的头像、名称和 AI 员工标签。 */
import { useTranslation } from "react-i18next"

import { WorkspaceIdentityType } from "@/api"
import { ProfileAvatar } from "@/components/profile-avatar"
import type { ChatTarget } from "@/features/inbox/shared/list-all-member-options"

/** 展示成员头像、名称，AI 员工另显示个人或企业标签。 */
export function GroupMemberOptionContent({ member }: { member: ChatTarget }) {
  const { t } = useTranslation("inbox")
  const agent = member.type === WorkspaceIdentityType.Agent
  return (
    <>
      <ProfileAvatar
        imageURL={member.avatarUrl}
        name={member.displayName}
        fallback={agent ? "agent" : "person"}
        className="size-9"
      />
      <span className="min-w-0 flex-1 truncate text-sm">{member.displayName}</span>
      {agent ? (
        <span className="shrink-0 text-xs text-muted-foreground">
          {t(member.personal ? "chatPickerPersonalAgent" : "groupAgent")}
        </span>
      ) : null}
    </>
  )
}
