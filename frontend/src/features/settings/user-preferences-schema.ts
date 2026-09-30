/** 偏好设置表单校验规则。 */
import { z } from "zod"

import { Locale } from "@/api"
import { themePreferences } from "@/features/settings/appearance-settings"

/** 创建偏好设置表单校验。 */
export function createUserPreferencesSchema(
  t: (key: "preferences.validation.timeZoneRequired") => string,
) {
  return z.object({
    locale: z.enum([
      Locale.LocaleChineseSimplified,
      Locale.LocaleEnglishUnitedStates,
    ]),
    translationLanguage: z.string(),
    timeZone: z.string().min(1, t("preferences.validation.timeZoneRequired")),
    theme: z.enum(themePreferences),
  })
}

export type UserPreferencesFormValues = z.infer<
  ReturnType<typeof createUserPreferencesSchema>
>
