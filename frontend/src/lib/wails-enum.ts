/** 处理 Wails 字符串枚举的空值。 */
import { z } from "zod"

type WailsStringEnum = Record<string, string> & { $zero: string }

type WailsEnumValue<Enum extends WailsStringEnum> = Enum[keyof Enum]

type NonZeroWailsEnum<Enum extends WailsStringEnum> = Exclude<
  WailsEnumValue<Enum>,
  Enum["$zero"]
>

/** 拒绝 Wails 字符串枚举的空值。 */
export function requiredWailsEnum<Enum extends WailsStringEnum>(
  values: Enum,
  message?: string,
) {
  return z.nativeEnum(values).refine(
    (value): boolean => value !== values.$zero,
    message,
  )
}

/** 将空值或未知取值解析为未选择。 */
export function optionalWailsEnum<Enum extends WailsStringEnum>(
  values: Enum,
  value: string | null,
): NonZeroWailsEnum<Enum> | undefined {
  if (value === null || value === values.$zero) {
    return undefined
  }
  return Object.values(values).includes(value)
    ? (value as NonZeroWailsEnum<Enum>)
    : undefined
}
