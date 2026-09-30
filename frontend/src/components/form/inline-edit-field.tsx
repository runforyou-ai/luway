/** 只读展示、就地编辑的表单字段。 */
import { type ComponentProps, type ReactNode, useRef, useState } from "react"
import {
  Controller,
  type Control,
  type FieldPathByValue,
  type FieldValues,
} from "react-hook-form"
import { useTranslation } from "react-i18next"

import { DetailEditRow } from "@/components/form/detail-edit-row"
import { Input } from "@/components/ui/input"

/**
 * 默认展示字段值，悬停出现编辑入口，点击后就地编辑，失焦或回车退出编辑并触发 onCommit。
 * 给出 onCancel 时 Esc 退出编辑并调用 onCancel，不触发 onCommit；否则 Esc 与回车相同。
 * 给出 editing 时编辑态由调用方控制，进入和退出编辑通过 onEditingChange 通知；editEnabled 为 false 时隐藏编辑入口。
 */
export function InlineEditField<T extends FieldValues>({
  control,
  name,
  label,
  required = false,
  format,
  empty,
  compact,
  editing: controlledEditing,
  editEnabled = true,
  onEditingChange,
  onCommit,
  onCancel,
  ...input
}: {
  control: Control<T>
  name: FieldPathByValue<T, string>
  label: string
  required?: boolean
  format?: (value: string) => ReactNode
  empty?: ReactNode
  compact?: boolean
  editing?: boolean
  editEnabled?: boolean
  onEditingChange?: (editing: boolean) => void
  onCommit?: () => void
  onCancel?: () => void
} & Omit<
  ComponentProps<typeof Input>,
  "name" | "value" | "defaultValue" | "onChange" | "onBlur" | "ref" | "required"
>) {
  const { t } = useTranslation("common")
  const [localEditing, setLocalEditing] = useState(false)
  const editing = controlledEditing ?? localEditing
  // Esc 取消后输入框失焦时跳过提交。
  const cancelled = useRef(false)

  /** 切换编辑态并通知调用方。 */
  function setEditing(next: boolean) {
    if (controlledEditing === undefined) setLocalEditing(next)
    onEditingChange?.(next)
  }

  return (
    <Controller
      control={control}
      name={name}
      render={({ field, fieldState }) => (
        <DetailEditRow
          label={label}
          required={required}
          compact={compact}
          editing={editing}
          editEnabled={!editing && editEnabled}
          onEdit={() => {
            cancelled.current = false
            setEditing(true)
          }}
          value={
            field.value ? (
              (format?.(field.value) ?? field.value)
            ) : (
              (empty ?? <span className="text-muted-foreground">{t("notSet")}</span>)
            )
          }
        >
          <Input
            {...input}
            autoFocus
            name={field.name}
            value={field.value}
            required={required}
            aria-invalid={fieldState.invalid}
            onChange={field.onChange}
            onBlur={() => {
              field.onBlur()
              if (cancelled.current) return
              setEditing(false)
              onCommit?.()
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape" && onCancel) {
                event.preventDefault()
                event.stopPropagation()
                cancelled.current = true
                onCancel()
                setEditing(false)
                return
              }
              if (event.key === "Enter" || event.key === "Escape") {
                event.preventDefault()
                event.currentTarget.blur()
              }
            }}
            ref={field.ref}
          />
        </DetailEditRow>
      )}
    />
  )
}
