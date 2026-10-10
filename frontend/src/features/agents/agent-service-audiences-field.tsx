/** AI 员工服务对象勾选字段。 */
import { useId } from "react"
import { useTranslation } from "react-i18next"

import { ServiceAudience } from "@/api"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"

/** 客户与员工两种服务对象的展示顺序。 */
const audienceChoices = [
  ServiceAudience.Customer,
  ServiceAudience.Employee,
] as const

/** 新建时「仅自己」选项的状态：与客户、员工互斥，未注册本机电脑时不可选，locked 时保持当前勾选状态不可改。 */
export type PersonalAudienceChoice = {
  checked: boolean
  available: boolean
  locked?: boolean
  onChange: (checked: boolean) => void
}

/** 以一组复选框选择 AI 员工服务的客户和员工；新建时传入 personal 显示与之互斥的「仅自己」，serviceDisabled 时客户与员工不可选。 */
export function AgentServiceAudiencesField({
  value,
  onChange,
  onBlur,
  disabled = false,
  serviceDisabled = false,
  personal,
}: {
  value: ServiceAudience[]
  onChange: (audiences: ServiceAudience[]) => void
  onBlur: () => void
  disabled?: boolean
  serviceDisabled?: boolean
  personal?: PersonalAudienceChoice
}) {
  const { t } = useTranslation("agents")
  const labelId = useId()
  const descriptionId = useId()
  const checkboxClassName = "size-4 accent-primary disabled:cursor-not-allowed disabled:opacity-50"
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
              className={checkboxClassName}
              disabled={disabled || serviceDisabled}
              checked={!personal?.checked && value.includes(audience)}
              onBlur={onBlur}
              onChange={(event) =>
                onChange(
                  event.target.checked
                    ? audienceChoices.filter(
                        (choice) => choice === audience || (!personal?.checked && value.includes(choice)),
                      )
                    : value.filter((choice) => choice !== audience),
                )
              }
            />
            <span>{t(`form.audiences.${audience}`)}</span>
          </label>
        ))}
        {personal ? (
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              className={checkboxClassName}
              disabled={disabled || !personal.available || personal.locked}
              checked={personal.checked}
              onBlur={onBlur}
              onChange={(event) => personal.onChange(event.target.checked)}
            />
            <span>{t("form.audiences.personal")}</span>
          </label>
        ) : null}
      </div>
      <FieldDescription id={descriptionId}>
        {t("form.serviceAudiencesHelp")}
        {personal ? t(personal.available ? "form.personalAudienceHelp" : "form.personalAudienceDesktopOnly") : null}
      </FieldDescription>
    </Field>
  )
}
