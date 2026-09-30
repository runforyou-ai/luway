/** 联网搜索设置表单校验规则。 */
import { z } from "zod"

import { WebSearchProvider } from "@/api"

/** 创建联网搜索设置校验：云端服务商必填 API 密钥，自托管服务必填 HTTP 或 HTTPS 服务地址。 */
export function createWebSearchSchema(messages: {
  apiKeyRequired: string
  baseUrlRequired: string
  baseUrlInvalid: string
}) {
  return z
    .object({
      provider: z.string(),
      apiKey: z.string(),
      baseUrl: z.string(),
    })
    .superRefine((values, context) => {
      if (values.provider === "") return
      if (values.provider !== WebSearchProvider.WebSearchProviderSearXNG) {
        if (values.apiKey.trim() === "") {
          context.addIssue({ code: "custom", path: ["apiKey"], message: messages.apiKeyRequired })
        }
        return
      }
      const baseUrl = values.baseUrl.trim()
      if (baseUrl === "") {
        context.addIssue({ code: "custom", path: ["baseUrl"], message: messages.baseUrlRequired })
        return
      }
      // 服务地址只接受 http 与 https 的绝对地址。
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

export type WebSearchFormValues = z.infer<ReturnType<typeof createWebSearchSchema>>
