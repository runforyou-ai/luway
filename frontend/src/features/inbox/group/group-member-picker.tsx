/** 建群和添加成员共用的候选搜索与受控多选区域。 */
import { useId, type Ref } from "react"
import { SearchIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { GroupMemberOptionContent } from "@/features/inbox/group/group-member-option-content"
import type { ChatTarget } from "@/features/inbox/shared/list-all-member-options"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"

/** 展示候选成员、搜索状态和选择上限，表单与提交由外层管理。 */
export function GroupMemberPicker({
  members, selected, onChange, query, onQueryChange, selectionLimit, label, emptyMessage,
  loading, error, onRetry, disabled = false, required = false,
  showCount = false, inputRef, name, onBlur,
}: {
  members: ChatTarget[]
  label: string
  emptyMessage: string
  selected: string[]
  onChange: (ids: string[]) => void
  query: string
  onQueryChange: (query: string) => void
  selectionLimit: number
  loading: boolean
  error: boolean
  onRetry: () => void
  disabled?: boolean
  required?: boolean
  showCount?: boolean
  inputRef?: Ref<HTMLInputElement>
  name?: string
  onBlur?: () => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const searchID = useId()
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const candidates = members.filter((member) =>
    member.displayName.toLocaleLowerCase().includes(normalizedQuery),
  )
  return (
    <div className="grid min-h-0 gap-2">
      <div className="flex items-center justify-between gap-3">
        <FieldLabel htmlFor={searchID} required={required}>
          {label}
        </FieldLabel>
        {showCount ? (
          <span className="text-xs text-muted-foreground">
            {t("groupMembersSelected", { count: selected.length })}
          </span>
        ) : null}
      </div>
      <div className="relative">
        <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id={searchID}
          ref={inputRef}
          value={query}
          autoComplete="off"
          className="pl-9"
          disabled={disabled}
          onBlur={onBlur}
          onChange={(event) => onQueryChange(event.target.value)}
        />
      </div>
      <ScrollArea className="h-64 rounded-md border">
        {loading ? (
          <LoadingIndicator className="h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <div className="flex h-64 flex-col items-center justify-center p-6 text-center">
            <p className="text-sm text-muted-foreground">{t("groupMembersLoadError")}</p>
            <Button type="button" variant="outline" size="sm" className="mt-3" onClick={onRetry}>
              {t("common:actions.retry")}
            </Button>
          </div>
        ) : selectionLimit === 0 || !candidates.length ? (
          <p className="px-6 py-12 text-center text-sm text-muted-foreground">
            {selectionLimit === 0
              ? t("groupMemberLimitReached")
              : normalizedQuery ? t("membersNoMatches") : emptyMessage}
          </p>
        ) : (
          <div className="grid p-1.5">
            {candidates.map((member) => (
              <GroupMemberOption
                key={member.id}
                member={member}
                name={name}
                checked={selected.includes(member.id)}
                disabled={disabled || (!selected.includes(member.id) && selected.length >= selectionLimit)}
                onBlur={onBlur}
                onChange={(checked) => onChange(checked
                  ? [...selected, member.id]
                  : selected.filter((id) => id !== member.id))}
              />
            ))}
          </div>
        )}
      </ScrollArea>
    </div>
  )
}

/** 展示单个成员的勾选项和成员信息。 */
function GroupMemberOption({
  member, checked, disabled, name, onBlur, onChange,
}: {
  member: ChatTarget
  checked: boolean
  disabled: boolean
  name?: string
  onBlur?: () => void
  onChange: (checked: boolean) => void
}) {
  const { t } = useTranslation("inbox")
  return (
    <label className="flex items-center gap-3 rounded-md px-3 py-2.5 transition-colors hover:bg-muted">
      <input
        type="checkbox"
        name={name}
        checked={checked}
        disabled={disabled}
        className="size-4 rounded border-input accent-primary disabled:cursor-not-allowed disabled:opacity-60"
        aria-label={t("groupSelectMember", { name: member.displayName })}
        onBlur={onBlur}
        onChange={(event) => onChange(event.target.checked)}
      />
      <GroupMemberOptionContent member={member} />
    </label>
  )
}
