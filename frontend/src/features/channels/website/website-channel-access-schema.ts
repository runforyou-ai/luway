/** 网站渠道允许使用的网站表单校验规则。 */
import { z } from "zod"

const hostnamePattern =
  /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?))*(?::\d{1,5})?$/i

/** 把多行网站配置整理为非空行。 */
export function allowedHostLines(value: string) {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
}

/** 判断单行网站配置是否为支持的域名或地址。 */
function isAllowedHost(value: string) {
  if (value === "*") {
    return true
  }
  let candidate = value
  if (/^https?:\/\//i.test(candidate)) {
    try {
      const parsed = new URL(candidate)
      if (parsed.username || parsed.password) {
        return false
      }
      candidate = parsed.host
    } catch {
      return false
    }
  } else if (/[/?#@]/.test(candidate)) {
    return false
  }
  candidate = candidate.replace(/^\*?\./, "")
  const [hostname, port, extra] = candidate.split(":")
  if (extra !== undefined || !hostnamePattern.test(candidate)) {
    return false
  }
  if (port !== undefined && (Number(port) < 1 || Number(port) > 65535)) {
    return false
  }
  return hostname.length <= 253 && candidate.length <= 253
}

/** 创建允许使用的网站表单校验。 */
export function createWebsiteChannelAccessSchema(messages: {
  tooMany: string
  invalid: string
}) {
  return z.object({
    allowedHosts: z
      .string()
      .refine((value) => allowedHostLines(value).length <= 50, {
        message: messages.tooMany,
      })
      .refine((value) => allowedHostLines(value).every(isAllowedHost), {
        message: messages.invalid,
      }),
  })
}

export type WebsiteChannelAccessFormValues = z.infer<
  ReturnType<typeof createWebsiteChannelAccessSchema>
>
