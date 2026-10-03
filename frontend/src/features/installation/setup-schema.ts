/** 首次安装表单校验规则。 */
import { z } from "zod"

import { workspaceNameField } from "@/lib/workspace-schema"
import { displayNamePattern } from "@/lib/display-name"

type SetupTranslator = (
  key:
    | "workspaceNameRequired"
    | "workspaceNameTooLong"
    | "displayNameRequired"
    | "displayNameInvalid"
    | "emailRequired"
    | "emailInvalid"
    | "passwordRequired"
    | "passwordTooShort"
    | "passwordTooLong",
) => string

/** 创建首次安装表单校验。 */
export function createSetupSchema(t: SetupTranslator) {
  const workspaceMessages = {
    nameRequired: t("workspaceNameRequired"),
    nameTooLong: t("workspaceNameTooLong"),
  }
  return z.object({
    workspaceName: workspaceNameField(workspaceMessages),
    displayName: z
      .string()
      .trim()
      .min(1, t("displayNameRequired"))
      .regex(displayNamePattern, t("displayNameInvalid")),
    email: z
      .string()
      .trim()
      .min(1, t("emailRequired"))
      .email(t("emailInvalid")),
    password: z
      .string()
      .min(1, t("passwordRequired"))
      .min(8, t("passwordTooShort"))
      .refine(
        (password) => new TextEncoder().encode(password).length <= 72,
        t("passwordTooLong"),
      ),
  })
}

export type SetupFormValues = z.infer<ReturnType<typeof createSetupSchema>>
