/** 企业服务器地址表单校验规则。 */
import { z } from "zod"

type ServerConnectionTranslator = (key: "serverUrlInvalid") => string

/** 创建企业服务器地址表单校验。 */
export function createServerConnectionSchema(t: ServerConnectionTranslator) {
  return z.object({
    protocol: z.enum(["https://", "http://"]),
    address: z
      .string()
      .trim()
      .min(1, t("serverUrlInvalid"))
      .refine((value) => {
        // 协议由选择框提供，地址保留域名、端口和部署路径。
        try {
          const url = new URL(`https://${value}`)
          return (
            Boolean(url.hostname) &&
            !/[\s\\]/.test(value) &&
            !value.includes("://") &&
            !url.username && !url.password && !url.search && !url.hash
          )
        } catch {
          return false
        }
      }, t("serverUrlInvalid")),
  })
}

export type ServerConnectionFormValues = z.infer<
  ReturnType<typeof createServerConnectionSchema>
>

/** 将完整服务器地址拆成协议与可编辑地址，未带协议时使用 HTTPS。 */
export function splitServerURL(value: string): ServerConnectionFormValues {
  const address = value.trim().replace(/\/+$/, "")
  const protocol = /^http:\/\//i.test(address) ? "http://" : "https://"
  return { protocol, address: address.replace(/^https?:\/\//i, "") }
}

/** 按所选协议与输入地址组成完整服务器地址。 */
export function serverConnectionURL(values: ServerConnectionFormValues): string {
  return `${values.protocol}${values.address.trim().replace(/\/+$/, "")}`
}
