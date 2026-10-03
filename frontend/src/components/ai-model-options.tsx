/** 按用途读取可选模型，平台模型与各供应商的工作区模型分组渲染下拉选项，选项值为模型编号，平台模型附带积分价格。 */
import { useTranslation } from "react-i18next"

import {
  listAIModelOptions,
  type AIModelOptionData,
  type AIModelUsageId,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useCreditFormat } from "@/hooks/use-credit-format"
import { useResource } from "@/hooks/use-resource"

/** 读取当前工作区满足指定用途的模型，每次挂载重新读取。 */
export function useAIModelOptions(usage: AIModelUsageId) {
  return useResource(
    resourceKeys.aiModelOptions(usage),
    () => listAIModelOptions(usage),
    { staleTime: 0 },
  )
}

/** 返回模型的展示名称：工作区模型带供应商名称，平台模型只显示模型名称。 */
export function aiModelLabel(model: Pick<AIModelOptionData, "name"> & { provider: { name: string } | null }) {
  return model.provider ? `${model.provider.name} · ${model.name}` : model.name
}

/** 平台模型归入同一组并在名称后显示积分价格，工作区模型按供应商分组，保持读取顺序。 */
export function AIModelOptionGroups({
  models,
}: {
  models: readonly AIModelOptionData[]
}) {
  const { t } = useTranslation("common")
  const credit = useCreditFormat()
  const groups = new Map<string, { label: string; models: AIModelOptionData[] }>()
  for (const model of models) {
    const key = model.provider?.id ?? ""
    const group = groups.get(key)
    if (group) {
      group.models.push(model)
    } else {
      groups.set(key, {
        label: model.provider?.name ?? t("aiModels.platformGroup"),
        models: [model],
      })
    }
  }
  return [...groups.entries()].map(([key, group]) => (
    <optgroup key={key} label={group.label}>
      {group.models.map((model) => (
        <option key={model.id} value={model.id}>
          {model.price ? t("aiModels.pricedOption", { name: model.name, price: credit.price(model.price, model.type) }) : model.name}
        </option>
      ))}
    </optgroup>
  ))
}
