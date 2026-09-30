/** 消息渠道基础信息表单校验规则。 */
import { z } from "zod"

import { ChannelType, CustomerLocale } from "@/api"
import {
  createChannelReceptionFields,
  validateChannelReceptionFallback,
} from "@/features/channels/reception/channel-reception-schema"
import { unicodeLength } from "@/lib/text-length"
import { requiredWailsEnum } from "@/lib/wails-enum"

/** 创建消息渠道基础信息校验。 */
export function createMessageChannelSchema(messages: {
  nameRequired: string
  nameTooLong: string
  descriptionTooLong: string
  teamRequired: string
  memberRequired: string
  fallbackDifferent: string
}) {
  return z
    .object({
      type: requiredWailsEnum(ChannelType),
      name: z
        .string()
        .trim()
        .min(1, messages.nameRequired)
        .refine((value) => unicodeLength(value) <= 100, {
          message: messages.nameTooLong,
        }),
      description: z
        .string()
        .trim()
        .refine((value) => unicodeLength(value) <= 2000, {
          message: messages.descriptionTooLong,
        }),
      defaultLocale: requiredWailsEnum(CustomerLocale),
      ...createChannelReceptionFields(messages),
    })
    .superRefine((value, context) =>
      validateChannelReceptionFallback(
        value,
        context,
        messages.fallbackDifferent,
      ),
    )
}

export type MessageChannelFormValues = z.infer<
  ReturnType<typeof createMessageChannelSchema>
>
