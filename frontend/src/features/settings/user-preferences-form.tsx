/** 用户偏好设置表单。 */
import { useEffect, useMemo } from "react"
import { useTheme } from "next-themes"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import { Locale, updateUserPreferences, type CurrentUser } from "@/api"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { AppearanceSettings, type ThemePreference } from "@/features/settings/appearance-settings"
import {
  userPreferencesSchema,
  type UserPreferencesFormValues,
} from "@/features/settings/user-preferences-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { changeAppLanguage } from "@/i18n"
import { languageDisplayName, translationLanguages } from "@/lib/languages"
import { supportedTimeZones } from "@/lib/time-zones"
import { zodResolver } from "@/lib/zod-resolver"

/** 修改当前用户的主题、界面语言、翻译语言和时区偏好。 */
export function UserPreferencesForm({ user }: { user: CurrentUser }) {
  const { t, i18n } = useTranslation(["settings", "common"])
  const invalidate = useResourceInvalidator()
  const { theme, setTheme } = useTheme()

  const timeZones = useMemo(
    () => supportedTimeZones(user.timeZone),
    [user.timeZone],
  )
  const form = useForm<UserPreferencesFormValues>({
    resolver: zodResolver(userPreferencesSchema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      locale: user.locale as UserPreferencesFormValues["locale"],
      translationLanguage: user.translationLanguage,
      timeZone: user.timeZone,
      theme: (theme ?? "system") as ThemePreference,
    },
  })
  // next-themes 初始化后同步未编辑的主题字段。
  useEffect(() => {
    if (!form.formState.dirtyFields.theme) {
      form.setValue("theme", (theme ?? "system") as ThemePreference, {
        shouldDirty: false,
      })
    }
  }, [form, theme])

  const { submit } = useFormSave({
    form,
    schema: userPreferencesSchema,
    autoSave: true,
    save: async (values) => {
      const updated = await updateUserPreferences({
        locale: values.locale,
        translationLanguage: values.translationLanguage,
        timeZone: values.timeZone,
        // 账号偏好接口需要完整提交，新消息提醒由通知设置页维护，这里沿用当前值。
        messageNotificationsEnabled: user.messageNotificationsEnabled,
      })
      setTheme(values.theme)
      const next = {
        locale: values.locale,
        translationLanguage: updated.translationLanguage,
        timeZone: updated.timeZone,
        theme: values.theme,
      }
      void invalidate(resourceKeys.identity())
      await changeAppLanguage(updated.locale)
      return next
    },
    savedValues: (saved) => saved,
    errorMessage: t("preferences.saveError"),
    errorFields: ["locale", "translationLanguage", "timeZone"],
    logLabel: "保存偏好设置",
  })

  return (
    <form
      className="w-full"
      aria-label={t("preferences.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup className="grid">
        <Controller
          name="theme"
          control={form.control}
          render={({ field, fieldState }) => (
            <AppearanceSettings
              name={field.name}
              value={field.value}
              invalid={fieldState.invalid}
              onBlur={field.onBlur}
              onChange={field.onChange}
            />
          )}
        />
        <Controller
          name="locale"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("preferences.language")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              >
                <option value={Locale.ChineseSimplified}>
                  {t("preferences.languages.zhCN")}
                </option>
                <option value={Locale.EnglishUnitedStates}>
                  {t("preferences.languages.enUS")}
                </option>
              </NativeSelect>
            </Field>
          )}
        />
        <Controller
          name="translationLanguage"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name}>
                {t("preferences.translationLanguage")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              >
                <option value="">{t("preferences.translationLanguageFollow")}</option>
                {/* 已保存的语言不在常用列表中时补充为选项。 */}
                {[
                  ...(field.value && !translationLanguages.includes(field.value) ? [field.value] : []),
                  ...translationLanguages,
                ].map((language) => (
                  <option key={language} value={language}>
                    {languageDisplayName(language, i18n.language)}
                  </option>
                ))}
              </NativeSelect>
              <FieldDescription>{t("preferences.translationLanguageDescription")}</FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="timeZone"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("preferences.timeZone")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              >
                {timeZones.map((timeZone) => (
                  <option key={timeZone} value={timeZone}>
                    {timeZone}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}
