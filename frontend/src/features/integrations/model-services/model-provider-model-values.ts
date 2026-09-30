/** 模型目录条目与表单值之间的转换和目录回滚。 */
import type { AIProviderModelData } from "@/api"

/** 把模型 Token 数转换为紧凑显示值。 */
function formatTokenCount(value: number) {
  if (value % 1_048_576 === 0) return `${value / 1_048_576}M`
  if (value % 1024 === 0) return `${value / 1024}K`
  return String(value)
}

/** 把模型契约转换为表单值。 */
export function modelFormValue(model: AIProviderModelData) {
  return {
    identifier: model.identifier,
    name: model.name,
    type: model.type,
    inputModalities: model.inputModalities,
    contextWindow:
      model.contextWindow > 0 ? formatTokenCount(model.contextWindow) : "",
    maxOutputTokens:
      model.maxOutputTokens > 0 ? formatTokenCount(model.maxOutputTokens) : "",
  }
}

/** 撤销一次被拒绝的目录改动：把 requested 相对 saved 的增删改从 current 中撤回，请求发出后的编辑保留当前值。 */
export function rollbackModels<T extends { identifier: string }>(
  saved: T[],
  requested: T[],
  current: T[],
) {
  const same = (left: T, right: T) => JSON.stringify(left) === JSON.stringify(right)
  const savedByID = new Map(saved.map((model) => [model.identifier, model]))
  const requestedByID = new Map(requested.map((model) => [model.identifier, model]))
  const result = current.flatMap((model) => {
    const request = requestedByID.get(model.identifier)
    if (!request || !same(model, request)) return [model]
    const original = savedByID.get(model.identifier)
    return original ? [original] : []
  })
  // 补回请求中移除且之后未重新添加的模型，尽量放回原位置。
  saved.forEach((model, index) => {
    if (
      requestedByID.has(model.identifier) ||
      result.some((item) => item.identifier === model.identifier)
    ) {
      return
    }
    result.splice(Math.min(index, result.length), 0, model)
  })
  return result
}
