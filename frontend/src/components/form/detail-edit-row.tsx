/** 详情页通用的字段级展示和编辑操作。 */
import type { ReactNode } from "react"
import { PencilIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { FieldRequiredMark } from "@/components/ui/field"
import { cn } from "@/lib/utils"

/** 详情行；给出 onEdit 时悬停显示编辑入口，始终处于编辑态时直接渲染控件。 */
export function DetailEditRow({
  label,
  value,
  editing,
  editEnabled,
  required = false,
  compact = false,
  onEdit,
  children,
}: {
  label: string
  value: ReactNode
  editing: boolean
  editEnabled: boolean
  required?: boolean
  compact?: boolean
  onEdit?: () => void
  children: ReactNode
}) {
  const { t } = useTranslation("common")
  const showEdit = !editing && editEnabled && Boolean(onEdit)

  return (
    <div
      className={cn(
        "group rounded-md px-2 transition-colors hover:bg-muted/50 focus-within:bg-muted/50",
        compact ? "py-1.5" : "py-2.5",
      )}
    >
      <div
        className={cn(
          "flex items-start gap-3",
          compact ? "min-h-8" : "min-h-9",
        )}
      >
        <div className="flex w-28 shrink-0 items-start gap-1 pt-1 text-sm text-muted-foreground">
          <span>{label}</span>
          {required ? <FieldRequiredMark /> : null}
        </div>
        <div className="min-w-0 flex-1">
          {editing ? (
            children
          ) : (
            <div className="pt-1 text-sm break-words">{value}</div>
          )}
        </div>
        <div className="flex w-8 shrink-0 justify-end">
          {showEdit ? (
            <Button
              variant="ghost"
              size="icon-sm"
              className="opacity-100 sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100"
              aria-label={t("actions.editField", { field: label })}
              title={t("actions.editField", { field: label })}
              onClick={onEdit}
            >
              <PencilIcon />
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  )
}

/** 详情字段编辑的保存和取消操作。 */
export function DetailEditActions({
  saving,
  onSave,
  onCancel,
}: {
  saving: boolean
  onSave: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation("common")
  return (
    <div className="mt-3 flex items-center gap-2">
      <Button size="sm" disabled={saving} onClick={onSave}>
        {saving ? t("actions.saving") : t("actions.save")}
      </Button>
      <Button variant="outline" size="sm" disabled={saving} onClick={onCancel}>
        {t("actions.cancel")}
      </Button>
    </div>
  )
}
