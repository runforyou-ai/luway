/** MCP 服务表单校验规则。 */
import { isHTTPURL } from "@/lib/http-url"
import { z } from "zod"

import { MCPServerType } from "@/api"

/** 创建 MCP 服务表单校验。 */
export function createMCPServerSchema(messages: {
  nameRequired: string
  nameTooLong: string
  serverTypeInvalid: string
  urlRequired: string
  urlTooLong: string
  urlInvalid: string
}) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, messages.nameRequired)
      .max(100, messages.nameTooLong),
    url: z
      .string()
      .trim()
      .min(1, messages.urlRequired)
      .max(2048, messages.urlTooLong)
      .refine(isHTTPURL, messages.urlInvalid),
    serverType: z.enum(MCPServerType, { error: messages.serverTypeInvalid }).refine(
      (value): boolean =>
        value === MCPServerType.MCPServerTypeSSE ||
        value === MCPServerType.MCPServerTypeStreamableHTTP,
      messages.serverTypeInvalid,
    ),
    authorizationToken: z.string(),
    customerScoped: z.boolean(),
  })
}

export type MCPServerFormValues = z.infer<
  ReturnType<typeof createMCPServerSchema>
>
