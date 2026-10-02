/** 表单共用的开关设置行。 */
import type { ReactNode, Ref } from "react"

import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from "@/components/ui/field"
import { Switch } from "@/components/ui/switch"

/** 以表单行展示开关，标题和说明在左、开关在右。 */
export function SwitchField({
  id,
  name,
  label,
  description,
  checked,
  disabled = false,
  onBlur,
  onCheckedChange,
  ref,
}: {
  id: string
  name: string
  label: ReactNode
  description?: ReactNode
  checked: boolean
  disabled?: boolean
  onBlur: () => void
  onCheckedChange: (checked: boolean) => void
  ref: Ref<HTMLButtonElement>
}) {
  return (
    <Field orientation="horizontal">
      <FieldContent>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        {description ? (
          <FieldDescription>{description}</FieldDescription>
        ) : null}
      </FieldContent>
      <Switch
        id={id}
        name={name}
        checked={checked}
        disabled={disabled}
        onBlur={onBlur}
        onCheckedChange={onCheckedChange}
        ref={ref}
      />
    </Field>
  )
}
