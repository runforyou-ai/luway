/** 偏好设置表单校验规则。 */
import { z } from "zod"

import { Locale } from "@/api"
import { themePreferences } from "@/features/settings/appearance-settings"

/** 偏好设置表单校验。 */
export const userPreferencesSchema = z.object({
  locale: z.enum([
    Locale.ChineseSimplified,
    Locale.EnglishUnitedStates,
  ]),
  translationLanguage: z.string(),
  timeZone: z.string().min(1),
  theme: z.enum(themePreferences),
})

export type UserPreferencesFormValues = z.infer<typeof userPreferencesSchema>
