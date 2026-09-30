/** AI 员工负责人选择字段。 */
import type { ComponentProps } from "react"
import { useTranslation } from "react-i18next"

import { UserStatus, listAllActiveUsers, type AgentResponsible } from "@/api"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 在在职成员中选择负责人，选项带邮箱；已保存的负责人始终保留为选项，停用时标注已停用。 */
export function AgentResponsibleField({
  responsible,
  invalid,
  ...select
}: Omit<ComponentProps<typeof NativeSelect>, "id" | "children"> & {
  responsible?: AgentResponsible | null
  invalid: boolean
}) {
  const { t } = useTranslation("agents")
  const members = useResource(resourceKeys.usersAll(), listAllActiveUsers, { staleTime: 0 })
  const options = (members.data ?? []).map((member) => ({
    id: member.id,
    label: t("form.responsibleOption", { name: member.displayName, email: member.email }),
  }))
  // 成员列表未就绪或负责人已停用时，按详情中的负责人补上当前选项。
  if (responsible && !options.some((option) => option.id === responsible.userId)) {
    const inactive = responsible.status === UserStatus.UserStatusInactive
    options.unshift({
      id: responsible.userId,
      label: t(inactive ? "form.responsibleInactiveOption" : "form.responsibleOption", {
        name: responsible.displayName,
        email: responsible.email,
      }),
    })
  }
  return (
    <Field data-invalid={invalid}>
      <FieldLabel htmlFor="agent-profile-responsible">{t("form.responsible")}</FieldLabel>
      <NativeSelect {...select} id="agent-profile-responsible" aria-invalid={invalid}>
        <option value="">{t("form.responsibleNone")}</option>
        {options.map((option) => (
          <option key={option.id} value={option.id}>
            {option.label}
          </option>
        ))}
      </NativeSelect>
      <FieldDescription>{t("form.responsibleHelp")}</FieldDescription>
    </Field>
  )
}
