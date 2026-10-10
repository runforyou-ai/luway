/** AI 员工启用的本机 Agent 字段。 */
import { useTranslation } from "react-i18next"

import type { ComputerLocalAgent } from "@/api"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"

/** 勾选 AI 员工可以委派的本机 Agent：选项为电脑上报的本机 Agent，已启用而电脑当前未提供的 Agent 仍列出并注明；电脑没有本机 Agent 且未启用任何 Agent 时不显示。 */
export function LocalAgentsField({
  id,
  available,
  value,
  onChange,
  disabled,
}: {
  id: string
  available: ComputerLocalAgent[]
  value: string[]
  onChange: (names: string[]) => void
  disabled: boolean
}) {
  const { t } = useTranslation("agents")
  const options = [
    ...available,
    ...value.filter((name) => !available.some((agent) => agent.name === name)).map((name) => ({ name, description: "" })),
  ]
  if (options.length === 0) return null
  return (
    <Field>
      <FieldLabel id={id}>{t("form.localAgents")}</FieldLabel>
      <div role="group" aria-labelledby={id} className="grid gap-2">
        {options.map((agent) => {
          const missing = !available.some((item) => item.name === agent.name)
          return (
            <label key={agent.name} className="flex min-w-0 items-start gap-2 text-sm">
              <input
                type="checkbox"
                className="mt-0.5 size-4 shrink-0 accent-primary"
                checked={value.includes(agent.name)}
                disabled={disabled}
                onChange={(event) =>
                  onChange(event.target.checked ? [...value, agent.name] : value.filter((name) => name !== agent.name))
                }
              />
              <span className="min-w-0">
                <span className="font-medium">{agent.name}</span>
                {missing ? (
                  <span className="ml-2 text-muted-foreground">{t("form.localAgentMissing")}</span>
                ) : agent.description ? (
                  <span className="block text-muted-foreground">{agent.description}</span>
                ) : null}
              </span>
            </label>
          )
        })}
      </div>
      <FieldDescription>{t("form.localAgentsHelp")}</FieldDescription>
    </Field>
  )
}
