/** 校验与解析契约枚举的取值。 */
import { z } from "zod"

type StringEnum = Readonly<Record<string, string>>

type EnumValue<Enum extends StringEnum> = Enum[keyof Enum]

/** 返回只接受契约枚举取值的校验规则。 */
export function enumSchema<Enum extends StringEnum>(values: Enum) {
  const members = Object.values(values) as [EnumValue<Enum>, ...EnumValue<Enum>[]]
  return z.enum(members)
}

/** 把查询参数等外部取值解析为契约枚举，空值或未知取值返回 undefined。 */
export function parseEnum<Enum extends StringEnum>(
  values: Enum,
  value: string | null | undefined,
): EnumValue<Enum> | undefined {
  return Object.values(values).includes(value ?? "") ? (value as EnumValue<Enum>) : undefined
}
