/** 客户会话回复区校验规则。 */
import { z } from "zod"

import { unicodeLength } from "@/lib/text-length"

/** 创建客户会话文本回复校验规则。 */
export function createConversationComposerSchema(messages: {
  bodyTooLong: string
}) {
  return z.object({
    body: z
      .string()
      .trim()
      .refine(
        (value) => {
          // 按 Unicode 字符计算文本长度。
          return unicodeLength(value) <= 4000
        },
        { message: messages.bodyTooLong },
      ),
  })
}

export type ConversationComposerValues = z.infer<
  ReturnType<typeof createConversationComposerSchema>
>
