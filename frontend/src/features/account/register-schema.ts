/** 注册表单校验规则。 */
import { z } from "zod"

import { displayNamePattern } from "@/lib/display-name"

type RegisterTranslator = (
  key:
    | "displayNameRequired"
    | "displayNameInvalid"
    | "emailRequired"
    | "emailInvalid"
    | "passwordRequired"
    | "passwordTooShort"
    | "passwordTooLong",
) => string

/** 创建注册表单校验。 */
export function createRegisterSchema(t: RegisterTranslator) {
  return z.object({
    displayName: z
      .string()
      .trim()
      .min(1, t("displayNameRequired"))
      .regex(displayNamePattern, t("displayNameInvalid")),
    email: z.string().trim().min(1, t("emailRequired")).email(t("emailInvalid")),
    password: z
      .string()
      .min(1, t("passwordRequired"))
      .min(8, t("passwordTooShort"))
      .refine((password) => new TextEncoder().encode(password).length <= 72, t("passwordTooLong")),
  })
}

export type RegisterFormValues = z.infer<ReturnType<typeof createRegisterSchema>>
