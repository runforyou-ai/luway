/** 通讯录表单共用的所属团队多选字段。 */
import { ChevronDownIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import type { Team } from "@/api"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/** 以下拉框样式展示已选团队，展开后勾选并把结果交回表单。 */
export function TeamSelectField({
  teams,
  label,
  emptyMessage,
  value,
  onChange,
  onBlur,
  disabled = false,
}: {
  teams: Team[]
  label: string
  emptyMessage: string
  value: string[]
  onChange: (ids: string[]) => void
  onBlur: () => void
  disabled?: boolean
}) {
  const { t } = useTranslation("contacts")
  const names = teams
    .filter((team) => value.includes(team.id))
    .map((team) => team.name)
    .join(t("teamSelect.separator"))
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      {teams.length === 0 ? (
        <FieldDescription>{emptyMessage}</FieldDescription>
      ) : (
        <DropdownMenu onOpenChange={(open) => !open && onBlur()}>
          <DropdownMenuTrigger
            type="button"
            data-slot="select-trigger"
            disabled={disabled}
            title={names || undefined}
            className="flex h-8 w-full min-w-0 items-center gap-2 rounded-md border border-input bg-transparent px-2.5 text-left text-sm shadow-xs transition-[color,box-shadow] outline-none focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30"
          >
            <span
              className={
                names
                  ? "min-w-0 flex-1 truncate"
                  : "min-w-0 flex-1 truncate text-muted-foreground"
              }
            >
              {names || t("teamSelect.placeholder")}
            </span>
            <ChevronDownIcon
              aria-hidden
              className="size-4 shrink-0 text-muted-foreground"
            />
          </DropdownMenuTrigger>
          {/* 浮层与触发框同宽并紧贴下方展开。 */}
          <DropdownMenuContent
            align="start"
            aria-label={label}
            className="max-h-72 w-(--radix-dropdown-menu-trigger-width)"
          >
            {teams.map((team) => (
              <DropdownMenuCheckboxItem
                key={team.id}
                checked={value.includes(team.id)}
                // 勾选后保持展开，便于连续选择多个团队。
                onSelect={(event) => event.preventDefault()}
                onCheckedChange={(checked) =>
                  onChange(
                    checked
                      ? [...value, team.id]
                      : value.filter((id) => id !== team.id),
                  )
                }
              >
                <span className="min-w-0 truncate">{team.name}</span>
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </Field>
  )
}
