/** AI 员工服务对象勾选字段。 */
import { useId } from "react"
import { useTranslation } from "react-i18next"

import { ServiceAudience } from "@/api"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"

const audienceChoices = [
  ServiceAudience.ServiceAudienceCustomer,
  ServiceAudience.ServiceAudienceEmployee,
] as const

/** 以一组复选框选择 AI 员工服务的客户和员工。 */
export function AgentServiceAudiencesField({
  value,
  onChange,
  onBlur,
  disabled = false,
}: {
  value: ServiceAudience[]
  onChange: (audiences: ServiceAudience[]) => void
  onBlur: () => void
  disabled?: boolean
}) {
  const { t } = useTranslation("agents")
  const labelId = useId()
  const descriptionId = useId()
  return (
    <Field>
      <FieldLabel id={labelId}>{t("form.serviceAudiences")}</FieldLabel>
      <div
        role="group"
        aria-labelledby={labelId}
        aria-describedby={descriptionId}
        className="flex flex-wrap gap-x-6 gap-y-2"
      >
        {audienceChoices.map((audience) => (
          <label key={audience} className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              className="size-4 accent-primary disabled:cursor-not-allowed disabled:opacity-50"
              disabled={disabled}
              checked={value.includes(audience)}
              onBlur={onBlur}
              onChange={(event) =>
                onChange(
                  event.target.checked
                    ? audienceChoices.filter(
                        (choice) => choice === audience || value.includes(choice),
                      )
                    : value.filter((choice) => choice !== audience),
                )
              }
            />
            <span>{t(`form.audiences.${audience}`)}</span>
          </label>
        ))}
      </div>
      <FieldDescription id={descriptionId}>
        {t("form.serviceAudiencesHelp")}
      </FieldDescription>
    </Field>
  )
}
