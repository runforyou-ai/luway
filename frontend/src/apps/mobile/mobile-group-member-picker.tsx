/** 移动端建群和添加群成员共用的真人与 AI 员工搜索和多选。 */
import { groupAdditionalMemberMaxCount } from "@/features/inbox/group-conversation-schema"
import { useId, useState, type Ref } from "react"
import { useTranslation } from "react-i18next"

import { OrganizationIdentityType, type MemberOption } from "@/api"
import { ProfileAvatar } from "@/components/profile-avatar"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { listChatTargets } from "@/features/inbox/list-all-member-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 保留跨搜索的列表勾选，建群时展示已选成员摘要；ownAssistantsOnly 时只列出本人名下的助理。 */
export function MobileGroupMemberPicker({
  currentIdentityID,
  selected,
  onChange,
  onBlur,
  inputRef,
  disabled,
  excludedIdentityIDs = [],
  ownAssistantsOnly = false,
  selectionLimit = groupAdditionalMemberMaxCount,
  showSelectionSummary = true,
  label,
}: {
  currentIdentityID: string
  selected: MemberOption[]
  onChange: (members: MemberOption[]) => void
  onBlur: () => void
  inputRef: Ref<HTMLInputElement>
  disabled: boolean
  excludedIdentityIDs?: string[]
  ownAssistantsOnly?: boolean
  selectionLimit?: number
  showSelectionSummary?: boolean
  label?: string
}) {
  const { t } = useTranslation(["mobile", "common"])
  const { t: tInbox } = useTranslation("inbox")
  const labelID = useId()
  const searchID = useId()
  const [search, setSearch] = useState("")
  const { data, loading, refreshing, error, refresh } = useResource(
    resourceKeys.chatTargets(),
    listChatTargets,
    { staleTime: 0 },
  )
  const members = (data ?? []).filter(
    (member) =>
      member.id !== currentIdentityID &&
      !excludedIdentityIDs.includes(member.id) &&
      (!ownAssistantsOnly || member.type === OrganizationIdentityType.OrganizationIdentityTypeAssistant),
  )
  const query = search.trim().toLocaleLowerCase()
  const candidates = members.filter((member) =>
    member.displayName.toLocaleLowerCase().includes(query),
  )

  return (
    <div className="space-y-2" role="group" aria-labelledby={labelID}>
      <div className="flex items-center justify-between gap-2">
        <FieldLabel id={labelID} required>
          {label ?? tInbox("groupMembersLabel")}
        </FieldLabel>
        <span className="text-xs text-muted-foreground" role="status">
          {tInbox("groupMembersSelected", { count: selected.length })}
        </span>
      </div>
      {showSelectionSummary ? (
        <div className="h-20 overflow-y-auto" aria-label={t("group.selectedMembers")}>
          {selected.length ? (
            <ul className="flex flex-wrap gap-2">
              {selected.map((member) => (
                <li key={member.id} className="max-w-full">
                  <Button
                    type="button"
                    variant="outline"
                    className="min-h-11 max-w-full"
                    disabled={disabled}
                    aria-label={t("group.removeMember", { name: member.displayName })}
                    onClick={() =>
                      onChange(selected.filter((item) => item.id !== member.id))
                    }
                  >
                    <span className="truncate">{member.displayName}</span>
                    <span className="text-muted-foreground">
                      {t("common:actions.remove")}
                    </span>
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="py-3 text-sm text-muted-foreground">
              {t("group.noSelection")}
            </p>
          )}
        </div>
      ) : null}
      <div className="space-y-2">
        <label htmlFor={searchID} className="text-sm">
          {t("group.searchMembers")}
        </label>
        <Input
          id={searchID}
          ref={inputRef}
          value={search}
          type="search"
          className="min-h-11 rounded-lg bg-muted/60 md:text-base dark:bg-muted/50"
          autoComplete="off"
          disabled={disabled}
          onBlur={onBlur}
          onChange={(event) => setSearch(event.target.value)}
        />
      </div>
      <div className="h-64 overflow-y-auto overscroll-contain rounded-md border">
        {loading && !data ? (
          <LoadingIndicator className="h-full justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : null}
        {error ? (
          <div className="space-y-3 p-4 text-sm">
            <p>{tInbox("groupMembersLoadError")}</p>
            <Button
              type="button"
              variant="outline"
              className="min-h-11"
              disabled={refreshing || disabled}
              onClick={() => void refresh()}
            >
              {t("common:actions.retry")}
            </Button>
          </div>
        ) : null}
        {data && !error && !candidates.length ? (
          <p className="p-4 text-sm text-muted-foreground">
            {tInbox("groupMembersEmpty")}
          </p>
        ) : null}
        <ul className="divide-y">
          {candidates.map((member) => {
            const checked = selected.some((item) => item.id === member.id)
            const agent =
              member.type === OrganizationIdentityType.OrganizationIdentityTypeAgent
            const assistant =
              member.type === OrganizationIdentityType.OrganizationIdentityTypeAssistant
            return (
              <li key={member.id}>
                <label className="flex min-h-16 items-center gap-3 px-3 py-2 active:bg-muted">
                  <input
                    type="checkbox"
                    checked={checked}
                    name="members"
                    className="size-5 shrink-0 accent-primary"
                    disabled={disabled || (!checked && selected.length >= selectionLimit)}
                    onBlur={onBlur}
                    onChange={(event) =>
                      onChange(
                        event.target.checked
                          ? [...selected, member]
                          : selected.filter((item) => item.id !== member.id),
                      )
                    }
                  />
                  <ProfileAvatar
                    name={member.displayName}
                    imageURL={member.avatarUrl}
                    fallback={agent || assistant ? "agent" : "person"}
                    className="size-9"
                  />
                  <span className="min-w-0 flex-1 truncate text-sm">{member.displayName}</span>
                  {agent || assistant ? (
                    <span className="shrink-0 text-xs text-muted-foreground">
                      {tInbox(agent ? "groupAgent" : "chatPickerAssistant")}
                    </span>
                  ) : null}
                </label>
              </li>
            )
          })}
        </ul>
      </div>
    </div>
  )
}
