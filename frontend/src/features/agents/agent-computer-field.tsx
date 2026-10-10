/** AI 员工使用的工作区电脑与授权字段。 */
import type { ComponentProps } from "react"
import { useTranslation } from "react-i18next"

import { OperationLevel, computerOperationLevels, listWorkspaceComputers, type AgentComputer } from "@/api"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { Switch } from "@/components/ui/switch"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 在工作区电脑中选择 AI 员工使用的电脑；列表未就绪时按详情中的电脑补上当前选项。 */
export function AgentComputerField({
  computer,
  invalid,
  ...select
}: Omit<ComponentProps<typeof NativeSelect>, "id" | "children"> & {
  computer?: AgentComputer | null
  invalid: boolean
}) {
  const { t } = useTranslation("agents")
  const computers = useResource(resourceKeys.workspaceComputers(), listWorkspaceComputers, { staleTime: 0 })
  const options = (computers.data?.computers ?? []).map((item) => ({ id: item.id, name: item.name }))
  if (computer && !options.some((option) => option.id === computer.id)) {
    options.unshift({ id: computer.id, name: computer.name })
  }
  return (
    <Field data-invalid={invalid}>
      <FieldLabel htmlFor="agent-profile-computer">{t("form.computer")}</FieldLabel>
      <NativeSelect {...select} id="agent-profile-computer" aria-invalid={invalid}>
        <option value="">{t("form.computerNone")}</option>
        {options.map((option) => (
          <option key={option.id} value={option.id}>
            {option.name}
          </option>
        ))}
      </NativeSelect>
      <FieldDescription>{t("form.computerHelp")}</FieldDescription>
    </Field>
  )
}

/** 工作区电脑授权的表单值。 */
export type ComputerGrantValue = { maxLevel: (typeof computerOperationLevels)[number]; confirmL2: boolean }

/** 设置 AI 员工在工作区电脑上允许的最高级别与修改文件前是否需要发起人确认；最高级别不含修改文件时不需要确认。 */
export function AgentComputerGrantField({
  value,
  onChange,
  disabled,
}: {
  value: ComputerGrantValue
  onChange: (grant: ComputerGrantValue) => void
  disabled: boolean
}) {
  const { t } = useTranslation("agents")
  const editable = value.maxLevel !== OperationLevel.L0
  return (
    <Field>
      <FieldLabel htmlFor="agent-profile-computer-level">{t("form.computerLevel")}</FieldLabel>
      <NativeSelect
        id="agent-profile-computer-level"
        value={value.maxLevel}
        disabled={disabled}
        // 降到只读时关闭修改文件前确认。
        onChange={(event) => {
          const maxLevel = event.target.value as ComputerGrantValue["maxLevel"]
          onChange({ maxLevel, confirmL2: maxLevel !== OperationLevel.L0 && value.confirmL2 })
        }}
      >
        {computerOperationLevels.map((level) => (
          <option key={level} value={level}>
            {t(`form.computerLevels.${level}`)}
          </option>
        ))}
      </NativeSelect>
      <div className="flex items-center gap-2">
        <Switch
          id="agent-profile-computer-confirm-l2"
          checked={value.confirmL2}
          disabled={disabled || !editable}
          onCheckedChange={(confirmL2) => onChange({ ...value, confirmL2 })}
        />
        <Label htmlFor="agent-profile-computer-confirm-l2" className="font-normal text-muted-foreground">
          {t("form.computerConfirmL2")}
        </Label>
      </div>
      <FieldDescription>{t("form.computerLevelHelp")}</FieldDescription>
    </Field>
  )
}
