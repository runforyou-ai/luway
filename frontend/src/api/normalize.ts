/** 生成类型的可空切片边界声明。 */

/** 递归把生成类型中的可空切片视为数组。 */
export type NonNullArrays<T> = [NonNullable<T>] extends [readonly (infer Element)[]]
  ? NonNullArrays<Element>[]
  : T extends object
    ? { [Key in keyof T]: NonNullArrays<T[Key]> }
    : T
