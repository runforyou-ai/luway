/** 偏好设置中的主题字段。 */
import { CheckIcon, MonitorIcon, MoonIcon, SunIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { FieldLegend, FieldSet } from "@/components/ui/field"
import { cn } from "@/lib/utils"

/** 偏好设置支持的主题。 */
export const themePreferences = ["system", "light", "dark"] as const

export type ThemePreference = (typeof themePreferences)[number]

const themeOptions = [
  { value: "system", icon: MonitorIcon },
  { value: "light", icon: SunIcon },
  { value: "dark", icon: MoonIcon },
] as const satisfies ReadonlyArray<{
  value: ThemePreference
  icon: typeof MonitorIcon
}>

/** 渲染主题选择字段。 */
export function AppearanceSettings({
  name,
  value,
  invalid,
  onBlur,
  onChange,
}: {
  name: string
  value: ThemePreference
  invalid: boolean
  onBlur: () => void
  onChange: (value: ThemePreference) => void
}) {
  const { t } = useTranslation("settings")

  return (
    <FieldSet className="w-full gap-0" aria-invalid={invalid}>
      <FieldLegend className="mb-0" variant="label">
        {t("appearance.theme")}
      </FieldLegend>
      <div
        className="mt-3 flex flex-wrap gap-2"
        data-slot="radio-group"
      >
        {themeOptions.map(({ value: optionValue, icon: Icon }) => {
          const checked = value === optionValue

          return (
            <label
              key={optionValue}
              className={cn(
                "flex min-w-28 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors",
                "hover:bg-accent/50 has-focus-visible:border-ring has-focus-visible:ring-ring/50 has-focus-visible:ring-[3px]",
                checked
                  ? "border-foreground/20 bg-accent text-accent-foreground"
                  : "border-border",
              )}
            >
              <input
                className="sr-only"
                type="radio"
                name={name}
                value={optionValue}
                checked={checked}
                aria-invalid={invalid}
                onBlur={onBlur}
                onChange={() => onChange(optionValue)}
              />
              <Icon className="size-4 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate font-medium">
                {t(`appearance.options.${optionValue}`)}
              </span>
              <CheckIcon
                className={cn(
                  "size-4 transition-opacity",
                  checked ? "opacity-100" : "opacity-0",
                )}
              />
            </label>
          )
        })}
      </div>
    </FieldSet>
  )
}
