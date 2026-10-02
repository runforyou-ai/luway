/** 按用途读取可选模型，并按供应商分组渲染下拉选项，选项值为模型编号。 */
import {
  listAIModelOptions,
  type AIModelOptionData,
  type AIModelUsageId,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 读取当前工作区满足指定用途的模型，每次挂载重新读取。 */
export function useAIModelOptions(usage: AIModelUsageId) {
  return useResource(
    resourceKeys.aiModelOptions(usage),
    () => listAIModelOptions(usage),
    { staleTime: 0 },
  )
}

/** 按供应商分组渲染模型选项，保持读取顺序。 */
export function AIModelOptionGroups({
  models,
}: {
  models: readonly AIModelOptionData[]
}) {
  const groups = new Map<
    string,
    { providerName: string; models: AIModelOptionData[] }
  >()
  for (const model of models) {
    const group = groups.get(model.providerId)
    if (group) {
      group.models.push(model)
    } else {
      groups.set(model.providerId, {
        providerName: model.providerName,
        models: [model],
      })
    }
  }
  return [...groups.entries()].map(([providerId, group]) => (
    <optgroup key={providerId} label={group.providerName}>
      {group.models.map((model) => (
        <option key={model.id} value={model.id}>
          {model.name}
        </option>
      ))}
    </optgroup>
  ))
}
