/** 移动端群成员头像预览、成员搜索列表和成员查看页。 */
import { useState, type ReactNode } from "react"
import { ChevronRightIcon, MinusIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate, useOutletContext } from "react-router"
import {
  GroupParticipantRole,
  OrganizationIdentityType,
  type GroupConversationData,
  type GroupParticipant,
} from "@/api"
import type { MobileGroupDetailsContext } from "@/apps/mobile/mobile-group-context"
import {
  MobilePageHeader,
  MobileScrollArea,
  MobileSearchBar,
} from "@/apps/mobile/mobile-page"
import { ProfileAvatar } from "@/components/profile-avatar"
import { useAssistantDisplayName } from "@/hooks/use-assistant-display-name"
import { isAIIdentityType } from "@/lib/identity-type"

/** 展示最多两行头像，以及群主添加和移除成员入口。 */
export function MobileGroupMembersPreview({
  group,
  showRemove,
  returnDepth,
  canAdd,
  canRemove,
}: {
  group: GroupConversationData
  showRemove: boolean
  returnDepth: number
  canAdd: boolean
  canRemove: boolean
}) {
  const { t } = useTranslation(["mobile", "inbox", "common"])
  const navigate = useNavigate()
  const visible = group.participants.slice(0, showRemove ? 8 : 9)
  return (
    <div className="border-b px-4 pt-5">
      <ul className="grid grid-cols-5 gap-x-3 gap-y-4">
        {visible.map((member) => (
          <li
            key={member.identityId}
            className="flex min-w-0 flex-col items-center gap-1.5"
          >
            <ProfileAvatar
              name={member.displayName}
              imageURL={member.avatarUrl}
              fallback={isAIIdentityType(member.identityType) ? "agent" : "person"}
              className="size-12 rounded-xl"
            />
            <span className="w-full truncate text-center text-xs">
              {member.displayName}
            </span>
            {/* 助理在名称下方展示主人。 */}
            {member.assistantOwnerName ? (
              <span className="-mt-1 w-full truncate text-center text-xs text-muted-foreground">
                {t("inbox:assistantOwnerLabel", { owner: member.assistantOwnerName })}
              </span>
            ) : null}
          </li>
        ))}
        {(showRemove ? (["add", "remove"] as const) : (["add"] as const)).map(
          (action) => (
            <li key={action} className="min-w-0">
              <button
                type="button"
                disabled={action === "add" ? !canAdd : !canRemove}
                onClick={() =>
                  navigate(action === "add" ? "add-members" : "remove-members", {
                    replace: returnDepth === 0,
                    state: {
                      mobileBack: returnDepth > 0,
                      groupReturnDepth: returnDepth > 0 ? returnDepth + 1 : 0,
                    },
                  })
                }
                className="flex w-full flex-col items-center gap-1.5 rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:text-muted-foreground disabled:opacity-50"
              >
                <span className="flex size-12 items-center justify-center rounded-xl border border-dashed">
                  {action === "add" ? (
                    <PlusIcon className="size-6" />
                  ) : (
                    <MinusIcon className="size-6" />
                  )}
                </span>
                <span className="text-xs">
                  {t(action === "add" ? "common:actions.add" : "common:actions.remove")}
                </span>
              </button>
            </li>
          ),
        )}
      </ul>
      <Link
        to="members"
        replace={returnDepth === 0}
        state={{
          mobileBack: returnDepth > 0,
          groupReturnDepth: returnDepth > 0 ? returnDepth + 1 : 0,
        }}
        className="mt-4 flex min-h-14 items-center justify-between gap-3 text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span>
          {t("group.viewMembers", { count: group.participants.length })}
        </span>
        <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" />
      </Link>
    </div>
  )
}

/** 按姓名搜索群成员，并在每行末尾渲染身份标记或操作。 */
export function MobileGroupMemberList({
  members,
  storageKey,
  emptyText,
  trailing,
}: {
  members: GroupParticipant[]
  storageKey: string
  emptyText: string
  trailing: (member: GroupParticipant) => ReactNode
}) {
  const { t } = useTranslation("inbox")
  const assistantDisplayName = useAssistantDisplayName()
  const [search, setSearch] = useState("")
  const query = search.trim().toLocaleLowerCase()
  const visible = members.filter((member) =>
    assistantDisplayName(member.displayName, member.assistantOwnerName).toLocaleLowerCase().includes(query),
  )
  return (
    <>
      <MobileSearchBar
        label={t("groupMemberSearch")}
        value={search}
        onChange={setSearch}
      />
      <MobileScrollArea storageKey={`${storageKey}:${search}`}>
        <ul className="divide-y">
          {visible.map((member) => (
            <li
              key={member.identityId}
              className="flex min-h-16 items-center gap-3 px-4 py-2"
            >
              <ProfileAvatar
                name={member.displayName}
                imageURL={member.avatarUrl}
                fallback={isAIIdentityType(member.identityType) ? "agent" : "person"}
                className="size-10"
              />
              <span className="min-w-0 flex-1 break-words text-sm">
                {assistantDisplayName(member.displayName, member.assistantOwnerName)}
              </span>
              {member.identityType === OrganizationIdentityType.OrganizationIdentityTypeAgent ? (
                <span className="shrink-0 text-xs text-muted-foreground">
                  {t("groupAgent")}
                </span>
              ) : null}
              {trailing(member)}
            </li>
          ))}
        </ul>
        {!visible.length ? (
          <p className="px-4 py-8 text-center text-sm text-muted-foreground">
            {query ? t("membersNoMatches") : emptyText}
          </p>
        ) : null}
      </MobileScrollArea>
    </>
  )
}

/** 在独立页面搜索和展示全部群成员及群主身份。 */
export function MobileGroupMembersPage() {
  const { t } = useTranslation(["inbox", "mobile"])
  const { group } = useOutletContext<MobileGroupDetailsContext>()
  return (
    <section className="flex h-full min-h-0 flex-col bg-background">
      <MobilePageHeader
        title={`${t("contextGroupMembersTab")}${t("mobile:group.memberCount", {
          count: group.participants.length,
        })}`}
        backTo={`/chats/group/${group.id}/details`}
      />
      <MobileGroupMemberList
        members={group.participants}
        storageKey={`group-members:${group.id}`}
        emptyText={t("membersNoMatches")}
        trailing={(member) =>
          member.role === GroupParticipantRole.GroupParticipantRoleOwner ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {t("groupOwner")}
            </span>
          ) : null
        }
      />
    </section>
  )
}
