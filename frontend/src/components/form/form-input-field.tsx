/** 带标签和校验状态的表单输入框。 */
import { type ComponentProps, type ReactNode, useState } from "react"
import { EyeIcon, EyeOffIcon } from "lucide-react"
import {
  Controller,
  type Control,
  type FieldPath,
  type FieldPathByValue,
  type FieldValues,
} from "react-hook-form"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

type FormInputFieldProps<T extends FieldValues> = {
  control: Control<T>
  name: FieldPathByValue<T, string>
  label: ReactNode
  description?: ReactNode
  id?: string
  required?: boolean
  deps?: FieldPath<T>[]
  endAction?: ReactNode
  passwordVisibilityLabels?: {
    show: string
    hide: string
  }
} & Omit<
  ComponentProps<typeof Input>,
  | "id"
  | "name"
  | "value"
  | "defaultValue"
  | "onChange"
  | "onBlur"
  | "ref"
  | "required"
  | "aria-invalid"
  | "aria-describedby"
>

/** 渲染受控表单输入，可切换密码可见性或附带末尾操作与帮助文案；deps 中的字段在本字段校验时一并重新校验。 */
export function FormInputField<T extends FieldValues>({
  control,
  name,
  label,
  description,
  id = name,
  required = true,
  deps,
  endAction,
  passwordVisibilityLabels,
  className,
  ...inputProps
}: FormInputFieldProps<T>) {
  const [passwordVisible, setPasswordVisible] = useState(false)

  return (
    <Controller
      name={name}
      control={control}
      rules={deps ? { deps } : undefined}
      render={({ field, fieldState }) => {
        const trailing = passwordVisibilityLabels ? (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="absolute top-1/2 right-1 -translate-y-1/2"
            aria-label={
              passwordVisible
                ? passwordVisibilityLabels.hide
                : passwordVisibilityLabels.show
            }
            aria-pressed={passwordVisible}
            onClick={() => setPasswordVisible((visible) => !visible)}
          >
            {passwordVisible ? <EyeOffIcon /> : <EyeIcon />}
          </Button>
        ) : (
          endAction
        )

        const input = (
          <Input
            {...field}
            {...inputProps}
            id={id}
            type={
              passwordVisibilityLabels
                ? passwordVisible
                  ? "text"
                  : "password"
                : inputProps.type
            }
            className={cn(trailing && "pr-10", className)}
            required={required}
            aria-invalid={fieldState.invalid}
          />
        )

        return (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor={id} required={required}>
              {label}
            </FieldLabel>
            {trailing ? (
              <div className="relative">
                {input}
                {trailing}
              </div>
            ) : (
              input
            )}
            {description ? <FieldDescription>{description}</FieldDescription> : null}
          </Field>
        )
      }}
    />
  )
}
