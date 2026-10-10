/** Telegram 连接表单校验规则。 */
import { z } from "zod"

import { TelegramConnectionMode } from "@/api"

/** Telegram 连接表单校验。 */
export const telegramChannelConnectionSchema = z.object({
  connectionMode: z.enum([
    TelegramConnectionMode.TelegramConnectionDirect,
    TelegramConnectionMode.TelegramConnectionGateway,
  ]),
  botToken: z.string().trim().min(1).max(512),
})

export type TelegramChannelConnectionFormValues = z.infer<
  typeof telegramChannelConnectionSchema
>
