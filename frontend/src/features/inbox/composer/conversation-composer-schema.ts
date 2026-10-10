/** 客户会话回复区校验规则。 */
import { z } from "zod"

import { unicodeLength } from "@/lib/text-length"

/** 创建客户会话文本回复校验规则，正文不超过 maxLength 个字符，maxBytes 大于 0 时 UTF-8 字节数也不超过它。 */
export function createConversationComposerSchema(messages: {
  bodyTooLong: string
  bodyTooManyBytes: string
}, maxLength = 4000, maxBytes = 0) {
  return z.object({
    body: z
      .string()
      .trim()
      .refine(
        (value) => {
          // 按 Unicode 字符计算文本长度。
          return unicodeLength(value) <= maxLength
        },
        { message: messages.bodyTooLong },
      )
      .refine(
        (value) => maxBytes <= 0 || new TextEncoder().encode(value).length <= maxBytes,
        { message: messages.bodyTooManyBytes },
      ),
  })
}

export type ConversationComposerValues = z.infer<
  ReturnType<typeof createConversationComposerSchema>
>
