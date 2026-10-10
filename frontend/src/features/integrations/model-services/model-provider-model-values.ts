/** 模型目录条目与表单值之间的转换和目录回滚。 */
import type { AIProviderModel } from "@/api"
import { formatTokenCount } from "@/components/model-provider/model-provider-schema"

/** 把模型契约转换为表单值，已保存模型以编号作为表单内标识。 */
export function modelFormValue(model: AIProviderModel) {
  return {
    key: model.id || crypto.randomUUID(),
    id: model.id,
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

/** 撤销一次被拒绝的目录改动：按表单内标识把 requested 相对 saved 的增删改从 current 中撤回，请求发出后的编辑保留当前值。 */
export function rollbackModels<T extends { key: string; identifier: string }>(
  saved: T[],
  requested: T[],
  current: T[],
) {
  const same = (left: T, right: T) => JSON.stringify(left) === JSON.stringify(right)
  const savedByKey = new Map(saved.map((model) => [model.key, model]))
  const requestedByKey = new Map(requested.map((model) => [model.key, model]))
  const result = current.flatMap((model) => {
    const request = requestedByKey.get(model.key)
    if (!request || !same(model, request)) return [model]
    const original = savedByKey.get(model.key)
    return original ? [original] : []
  })
  // 补回请求中移除且之后未重新添加同一模型或同名标识的模型，尽量放回原位置。
  saved.forEach((model, index) => {
    if (
      requestedByKey.has(model.key) ||
      result.some((item) => item.key === model.key || item.identifier === model.identifier)
    ) {
      return
    }
    result.splice(Math.min(index, result.length), 0, model)
  })
  return result
}
