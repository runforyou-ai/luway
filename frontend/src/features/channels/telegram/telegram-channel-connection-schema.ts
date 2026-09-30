/** Telegram 连接表单校验规则。 */
import { z } from "zod"

/** 创建 Telegram 连接表单校验。 */
export function createTelegramChannelConnectionSchema(messages: {
  tokenRequired: string
  tokenTooLong: string
}) {
  return z.object({
    botToken: z
      .string()
      .trim()
      .min(1, messages.tokenRequired)
      .max(512, messages.tokenTooLong),
  })
}

export type TelegramChannelConnectionFormValues = z.infer<
  ReturnType<typeof createTelegramChannelConnectionSchema>
>
