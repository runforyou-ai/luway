/** 网站渠道聊天窗口表单校验规则。 */
import { z } from "zod"

import { unicodeLength } from "@/lib/text-length"

/** 网站渠道聊天窗口的默认主题色。 */
export const defaultWebsiteChannelThemeColor = "#2563EB"

/** 判断主题色是否为六位十六进制颜色。 */
export function isWebsiteChannelThemeColor(
  value: string | undefined
): value is string {
  return /^#[0-9A-Fa-f]{6}$/.test(value ?? "")
}

/** 创建网站渠道聊天窗口校验。 */
export function createWebsiteChannelChatInterfaceSchema(messages: {
  titleTooLong: string
  greetingTooLong: string
  themeColorInvalid: string
}) {
  return z.object({
    title: z
      .string()
      .trim()
      .min(1)
      .refine((value) => unicodeLength(value) <= 100, {
        message: messages.titleTooLong,
      }),
    greetingMessage: z
      .string()
      .trim()
      .refine((value) => unicodeLength(value) <= 500, {
        message: messages.greetingTooLong,
      }),
    themeColor: z
      .string()
      .trim()
      .refine(isWebsiteChannelThemeColor, messages.themeColorInvalid),
    attachmentsEnabled: z.boolean(),
    emojiEnabled: z.boolean(),
    ratingEnabled: z.boolean(),
    multipleConversationsEnabled: z.boolean(),
  })
}

export type WebsiteChannelChatInterfaceFormValues = z.infer<
  ReturnType<typeof createWebsiteChannelChatInterfaceSchema>
>
