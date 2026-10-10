/** 业务系统表单校验规则。 */
import { isHTTPURL } from "@/lib/http-url"
import { z } from "zod"

import {
  BusinessSystemCredentialKind,
  BusinessSystemTransport,
  businessSystemCredentialKinds,
  businessSystemTransports,
  contextValues,
  mcpServerTypes,
  type BusinessSystem,
} from "@/api"

/** 请求头名称允许的字符，与 HTTP 字段名规则一致。 */
const headerNamePattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/

/** OpenAPI 文档的来源：文档地址或粘贴的文档正文。 */
export const specSources = ["url", "content"] as const

/** 创建业务系统表单校验：按传输方式校验连接字段，按认证方式校验凭据字段。 */
export function createBusinessSystemSchema(messages: {
  urlRequired: string
  urlTooLong: string
  urlInvalid: string
  specRequired: string
  credentialRequired: string
  headerNameInvalid: string
  headerNameDuplicate: string
  headerNameConflict: string
}) {
  /** 校验地址字段，required 为 false 时允许为空。 */
  function checkURL(context: z.RefinementCtx, path: string, value: string, required: boolean) {
    const trimmed = value.trim()
    if (!trimmed) {
      if (required) context.addIssue({ code: "custom", path: [path], message: messages.urlRequired })
    } else if (trimmed.length > 2048) {
      context.addIssue({ code: "custom", path: [path], message: messages.urlTooLong })
    } else if (!isHTTPURL(trimmed)) {
      context.addIssue({ code: "custom", path: [path], message: messages.urlInvalid })
    }
  }

  return z
    .object({
      name: z.string().trim().min(1).max(100),
      transport: z.enum(businessSystemTransports),
      url: z.string(),
      serverType: z.enum(mcpServerTypes),
      specSource: z.enum(specSources),
      specUrl: z.string(),
      spec: z.string(),
      baseUrl: z.string(),
      credentialKind: z.enum(businessSystemCredentialKinds),
      credentialHeaderName: z.string(),
      credentialToken: z.string(),
      credentialUsername: z.string(),
      credentialPassword: z.string(),
      headerBindings: z
        .array(
          z.object({
            header: z
              .string()
              .trim()
              .regex(headerNamePattern, messages.headerNameInvalid)
              .refine((value) => value.toLowerCase() !== "authorization", messages.headerNameInvalid),
            value: z.enum(contextValues),
          }),
        )
        .superRefine((bindings, context) => {
          // 请求头名称不区分大小写，重复的名称标记在后出现的一行。
          const seen = new Set<string>()
          bindings.forEach((binding, index) => {
            const name = binding.header.trim().toLowerCase()
            if (seen.has(name)) {
              context.addIssue({ code: "custom", path: [index, "header"], message: messages.headerNameDuplicate })
            }
            seen.add(name)
          })
        }),
    })
    .superRefine((values, context) => {
      if (values.transport === BusinessSystemTransport.MCP) {
        checkURL(context, "url", values.url, true)
      } else {
        checkURL(context, "baseUrl", values.baseUrl, false)
        if (values.specSource === "url") checkURL(context, "specUrl", values.specUrl, true)
        else if (!values.spec.trim()) context.addIssue({ code: "custom", path: ["spec"], message: messages.specRequired })
      }
      const kind = values.credentialKind
      if (kind === BusinessSystemCredentialKind.BusinessSystemCredentialHeader) {
        const name = values.credentialHeaderName.trim()
        if (!headerNamePattern.test(name) || name.toLowerCase() === "authorization") {
          context.addIssue({ code: "custom", path: ["credentialHeaderName"], message: messages.headerNameInvalid })
        }
        // 身份请求头不能与认证请求头同名。
        values.headerBindings.forEach((binding, index) => {
          if (name && binding.header.trim().toLowerCase() === name.toLowerCase()) {
            context.addIssue({ code: "custom", path: ["headerBindings", index, "header"], message: messages.headerNameConflict })
          }
        })
      }
      if ((kind === BusinessSystemCredentialKind.BusinessSystemCredentialBearer || kind === BusinessSystemCredentialKind.BusinessSystemCredentialHeader) && !values.credentialToken.trim()) {
        context.addIssue({ code: "custom", path: ["credentialToken"], message: messages.credentialRequired })
      }
      if (kind === BusinessSystemCredentialKind.BusinessSystemCredentialBasic && !values.credentialUsername.trim()) {
        context.addIssue({ code: "custom", path: ["credentialUsername"], message: messages.credentialRequired })
      }
    })
}

export type BusinessSystemFormValues = z.infer<ReturnType<typeof createBusinessSystemSchema>>

/** 业务系统表单的初始值：编辑时取自已保存的业务系统，新建时为 MCP、不认证。 */
export function businessSystemFormValues(system: BusinessSystem | null): BusinessSystemFormValues {
  return {
    name: system?.name ?? "",
    transport: system?.transport ?? BusinessSystemTransport.MCP,
    url: system?.mcp?.url ?? "",
    serverType: system?.mcp?.serverType ?? mcpServerTypes[0],
    specSource: system?.http && !system.http.specUrl ? "content" : "url",
    specUrl: system?.http?.specUrl ?? "",
    spec: system?.http?.spec ?? "",
    baseUrl: system?.http?.baseUrl ?? "",
    credentialKind: system?.credential.kind ?? BusinessSystemCredentialKind.BusinessSystemCredentialNone,
    credentialHeaderName: system?.credential.headerName ?? "",
    credentialToken: system?.credential.token ?? "",
    credentialUsername: system?.credential.username ?? "",
    credentialPassword: system?.credential.password ?? "",
    headerBindings: system?.headerBindings ?? [],
  }
}

/** 把表单值转为连接测试与保存共用的连接配置：只提交传输方式对应的一项与认证方式用到的凭据字段。 */
export function businessSystemConnection(values: BusinessSystemFormValues) {
  const mcp = values.transport === BusinessSystemTransport.MCP
  return {
    transport: values.transport,
    mcp: mcp ? { url: values.url, serverType: values.serverType } : null,
    http: mcp
      ? null
      : {
          baseUrl: values.baseUrl,
          specUrl: values.specSource === "url" ? values.specUrl : "",
          spec: values.specSource === "content" ? values.spec : "",
        },
    credential: {
      kind: values.credentialKind,
      headerName: values.credentialHeaderName,
      token: values.credentialToken,
      username: values.credentialUsername,
      password: values.credentialPassword,
    },
  }
}
