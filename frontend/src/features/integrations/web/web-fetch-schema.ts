/** 网页读取设置表单校验规则。 */
import { z } from "zod"

import { WebFetchProvider } from "@/api"

/** 创建网页读取设置校验：内置直连无需凭据，外部服务商必填 API 密钥，服务地址可选。 */
export function createWebFetchSchema(messages: {
  apiKeyRequired: string
  baseUrlInvalid: string
}) {
  return z
    .object({
      provider: z.string(),
      apiKey: z.string(),
      baseUrl: z.string(),
    })
    .superRefine((values, context) => {
      if (values.provider === WebFetchProvider.Direct || values.provider === "") return
      if (values.apiKey.trim() === "") {
        context.addIssue({ code: "custom", path: ["apiKey"], message: messages.apiKeyRequired })
      }
      const baseUrl = values.baseUrl.trim()
      if (baseUrl === "") return
      let protocol = ""
      try {
        protocol = new URL(baseUrl).protocol
      } catch {
        protocol = ""
      }
      if (protocol !== "http:" && protocol !== "https:") {
        context.addIssue({ code: "custom", path: ["baseUrl"], message: messages.baseUrlInvalid })
      }
    })
}

export type WebFetchFormValues = z.infer<ReturnType<typeof createWebFetchSchema>>
