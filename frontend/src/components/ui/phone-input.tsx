/** shadcn 国际电话号码输入。 */
import * as React from "react"
import { useTranslation } from "react-i18next"
import PhoneNumberInput, {
  type Props as PhoneNumberInputProps,
  type Value,
} from "react-phone-number-input"
import enLabels from "react-phone-number-input/locale/en"
import zhLabels from "react-phone-number-input/locale/zh"
import "react-phone-number-input/style.css"

import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

type PhoneInputProps = Omit<
  PhoneNumberInputProps<React.ComponentProps<"input">>,
  | "countryCallingCodeEditable"
  | "defaultCountry"
  | "inputComponent"
  | "international"
  | "labels"
  | "locales"
  | "onChange"
  | "value"
> & {
  value?: string
  onChange: (value: string) => void
}

const preferredCountries = [
  "CN",
  "HK",
  "MO",
  "TW",
  "US",
  "CA",
  "GB",
  "JP",
  "KR",
  "SG",
  "AU",
  "...",
] as const

const PhoneNumberField = React.forwardRef<
  HTMLInputElement,
  React.ComponentProps<"input">
>(({ className, ...props }, ref) => (
  <Input
    {...props}
    ref={ref}
    className={cn(
      className,
      "h-full rounded-l-none border-0 bg-transparent shadow-none focus-visible:ring-0 dark:bg-transparent",
    )}
  />
))
PhoneNumberField.displayName = "PhoneNumberField"

const PhoneInput = React.forwardRef<HTMLInputElement, PhoneInputProps>(
  ({ className, onChange, value, ...props }, ref) => {
    const { i18n } = useTranslation()
    const chinese = i18n.resolvedLanguage === "zh-CN"

    return (
      <PhoneNumberInput
        {...props}
        inputRef={ref}
        className={cn("app-phone-input", className)}
        value={value}
        onChange={(nextValue: Value | undefined) => onChange(nextValue ?? "")}
        defaultCountry={chinese ? "CN" : "US"}
        countryOptionsOrder={[...preferredCountries]}
        countrySelectProps={{ unicodeFlags: true }}
        countryCallingCodeEditable={false}
        international
        limitMaxLength
        labels={chinese ? zhLabels : enLabels}
        locales={chinese ? "zh-CN" : "en-US"}
        inputComponent={PhoneNumberField}
      />
    )
  },
)
PhoneInput.displayName = "PhoneInput"

export { PhoneInput }
