/** 登录表单校验规则。 */
import { z } from "zod"

type LoginTranslator = (
  key: "emailRequired" | "emailInvalid" | "passwordRequired",
) => string

/** 创建登录表单校验。 */
export function createLoginSchema(t: LoginTranslator) {
  return z.object({
    email: z
      .string()
      .trim()
      .min(1, t("emailRequired"))
      .email(t("emailInvalid")),
    password: z.string().min(1, t("passwordRequired")),
  })
}

export type LoginFormValues = z.infer<ReturnType<typeof createLoginSchema>>
