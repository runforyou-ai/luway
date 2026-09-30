/** 设置页按供应商分组的模型选项、选择值编码与下拉选项。 */
import { useTranslation } from "react-i18next"

import type { AgentModelOption, AIModelReference } from "@/api"

/** 按供应商分组的可选模型。 */
export type ModelGroup = { id: string; name: string; models: { identifier: string; name: string }[] }

/** 把可选对话模型按供应商分组，保持读取顺序。 */
export function groupChatModels(options: readonly AgentModelOption[]) {
  const groups: ModelGroup[] = []
  for (const model of options) {
    let group = groups.find((item) => item.id === model.providerId)
    if (!group) {
      group = { id: model.providerId, name: model.providerName, models: [] }
      groups.push(group)
    }
    group.models.push({ identifier: model.modelIdentifier, name: model.modelName })
  }
  return groups
}

/** 把模型引用编码为选择值，未设置时为空字符串。 */
export function modelValue(reference: AIModelReference | null) {
  return reference ? JSON.stringify([reference.providerId, reference.modelIdentifier]) : ""
}

/** 解析模型选择值，空字符串表示不使用。 */
export function modelReference(value: string): AIModelReference | null {
  if (!value) return null
  const [providerId, modelIdentifier] = JSON.parse(value) as [string, string]
  return { providerId, modelIdentifier }
}

/** 渲染「不使用」与按供应商分组的模型选项。 */
export function ModelGroupOptions({ groups }: { groups: readonly ModelGroup[] }) {
  const { t } = useTranslation("settings")
  return (
    <>
      <option value="">{t("customerService.models.notUsed")}</option>
      {groups.map((provider) => (
        <optgroup key={provider.id} label={provider.name}>
          {provider.models.map((model) => (
            <option key={model.identifier} value={modelValue({ providerId: provider.id, modelIdentifier: model.identifier })}>
              {model.name}
            </option>
          ))}
        </optgroup>
      ))}
    </>
  )
}
