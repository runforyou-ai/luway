/** 联系人界面的枚举文案。 */
import type { TFunction } from "i18next"

import { ContactStage, type ChannelType } from "@/api"
import { messageChannelTypeDefinition } from "@/lib/message-channel-types"

/** 渠道类型文案。 */
export function channelTypeLabel(
  type: ChannelType,
  t: TFunction<"contacts">,
) {
  const definition = messageChannelTypeDefinition(type)
  if (!definition) {
    console.warn("未知的渠道类型", type)
    return ""
  }
  return t(`channelTypes.${definition.translationKey}`)
}

/** 联系人阶段的选项顺序与翻译键。 */
export const contactStageOptions = [
  { value: ContactStage.ContactStageVisitor, label: "stages.visitor" },
  { value: ContactStage.ContactStageLead, label: "stages.lead" },
  { value: ContactStage.ContactStageCustomer, label: "stages.customer" },
] as const

/** 联系人阶段对应的翻译键，未知阶段返回 null。 */
export function contactStageKey(stage: ContactStage) {
  const option = contactStageOptions.find((item) => item.value === stage)
  if (!option) console.warn("未知的联系人阶段", stage)
  return option?.label ?? null
}
