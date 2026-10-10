/** 首次安装表单校验规则。 */
import { z } from "zod"

import { workspaceNameField } from "@/lib/workspace-schema"
import { displayNamePattern } from "@/lib/display-name"
import { isHTTPOrigin } from "@/lib/http-url"

type SetupTranslator = (
  key: "publicURLInvalid" | "displayNameInvalid" | "passwordTooLong",
) => string

/** 创建首次安装表单校验。 */
export function createSetupSchema(t: SetupTranslator) {
  return z.object({
    publicURL: z
      .string()
      .trim()
      .transform((value) => value.replace(/\/+$/, ""))
      .pipe(z.string().min(1).refine(isHTTPOrigin, t("publicURLInvalid"))),
    workspaceName: workspaceNameField,
    displayName: z
      .string()
      .trim()
      .min(1)
      .regex(displayNamePattern, t("displayNameInvalid")),
    email: z.string().trim().min(1).email(),
    password: z
      .string()
      .min(1)
      .min(8)
      .refine(
        (password) => new TextEncoder().encode(password).length <= 72,
        t("passwordTooLong"),
      ),
  })
}

export type SetupFormValues = z.infer<ReturnType<typeof createSetupSchema>>
