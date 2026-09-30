/** 企业服务器地址表单校验规则。 */
import { z } from "zod"

type ServerConnectionTranslator = (
  key: "serverUrlRequired" | "serverUrlInvalid",
) => string

/** 创建企业服务器地址表单校验。 */
export function createServerConnectionSchema(t: ServerConnectionTranslator) {
  return z.object({
    serverUrl: z
      .string()
      .trim()
      .min(1, t("serverUrlRequired"))
      .refine((value) => {
        // 判断企业服务器地址是否为有效的 http(s) URL。
        try {
          const url = new URL(value)
          return (
            Boolean(url.hostname) &&
            ["http:", "https:"].includes(url.protocol)
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
