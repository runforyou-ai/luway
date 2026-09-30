/** 带嵌套字段原生校验提示的 Zod 表单解析器。 */
import { zodResolver as baseZodResolver } from "@hookform/resolvers/zod"
import { get, type FieldErrors, type FieldValues, type Resolver } from "react-hook-form"
import type { z } from "zod"

type NativeValidityRef = { setCustomValidity: (message: string) => void; reportValidity: () => boolean }

/** 判断引用是否支持原生校验提示。 */
function hasNativeValidity(ref: unknown): ref is NativeValidityRef {
  return typeof ref === "object" && ref !== null && "reportValidity" in ref
}

/** 按字段路径遍历参与校验的字段，为带原生校验能力的控件写入对应错误文案，无错误时清空；shouldReport 返回 true 的无效字段弹出提示。 */
function applyNativeValidity(fields: object, errors: FieldErrors, shouldReport: (name: string) => boolean) {
  for (const value of Object.values(fields)) {
    if (typeof value !== "object" || value === null) continue
    if ("name" in value && typeof value.name === "string" && "ref" in value) {
      const refs: unknown[] = "refs" in value && Array.isArray(value.refs) ? value.refs : [value.ref]
      const message = get(errors, value.name)?.message
      const invalid = typeof message === "string" && message !== ""
      const report = invalid && shouldReport(value.name)
      for (const ref of refs) {
        if (!hasNativeValidity(ref)) continue
        ref.setCustomValidity(invalid ? message : "")
        if (report) ref.reportValidity()
      }
      continue
    }
    applyNativeValidity(value, errors, shouldReport)
  }
}

/** 按 Zod schema 校验表单，启用原生校验时为数组与嵌套对象中的字段写入浏览器提示，只在正在编辑的字段或第一个无效字段弹出提示。 */
export function zodResolver<Input extends FieldValues, Output, T extends z.ZodType<Output, Input>>(
  schema: T,
): Resolver<z.input<T>, unknown, z.output<T>> {
  const resolve = baseZodResolver<Input, unknown, Output, T>(schema)
  return async (values, context, options) => {
    const result = await resolve(values, context, { ...options, shouldUseNativeValidation: false })
    if (options.shouldUseNativeValidation) {
      // 焦点在本次校验的字段上时只提示该字段，其余字段只写入文案，焦点保持不动；焦点在按钮等其他位置时只提示第一个无效字段。
      const focusedName = document.activeElement?.getAttribute("name")
      const editing = focusedName != null && options.names?.some((name) => name === focusedName)
      let reported = false
      applyNativeValidity(options.fields, result.errors, (name) => {
        if (editing) return name === focusedName
        if (reported) return false
        reported = true
        return true
      })
    }
    return result
  }
}
