/** 个人资料表单校验规则。 */
import { z } from "zod"

import { displayNamePattern } from "@/lib/display-name"

type ProfileTranslator = (key: "profile.validation.displayNameInvalid") => string

/** 创建个人资料表单校验。 */
export function createProfileSettingsSchema(t: ProfileTranslator) {
  return z.object({
    displayName: z
      .string()
      .trim()
      .min(1)
      .regex(displayNamePattern, t("profile.validation.displayNameInvalid")),
    email: z.string().trim().min(1).email(),
  })
}

export type ProfileSettingsFormValues = z.infer<
  ReturnType<typeof createProfileSettingsSchema>
>
