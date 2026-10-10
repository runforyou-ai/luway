/** AI 员工表单中的业务系统授权：选择业务系统并逐个设置可执行的最高级别、L2 操作是否需要确认与是否允许对外发信。 */
import { useTranslation } from "react-i18next"

import {
  OperationLevel,
  listAgentBusinessSystemOptions,
  operationLevels,
  type AgentBusinessSystemGrant,
} from "@/api"
import { AgentResourcePickerField } from "@/components/agent-fields/agent-resource-picker-field"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { Switch } from "@/components/ui/switch"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 判断最高级别包含 L2 操作。 */
function includesL2(level: OperationLevel) {
  return operationLevels.indexOf(level) >= operationLevels.indexOf(OperationLevel.L2)
}

/** 在模态框中选择业务系统，新选的业务系统默认只查询本人数据，已选的业务系统逐行设置最高级别、L2 操作是否需要确认与是否允许对外发信。 */
export function AgentBusinessSystemsField({
  value,
  onChange,
  disabled,
}: {
  value: AgentBusinessSystemGrant[]
  onChange: (grants: AgentBusinessSystemGrant[]) => void
  disabled: boolean
}) {
  const { t } = useTranslation(["agents", "integrations"])
  const options = useResource(resourceKeys.agentBusinessSystemOptions(), () => listAgentBusinessSystemOptions(), { staleTime: 0 })
  const names = new Map((options.data ?? []).map((option) => [option.id, option.name]))
  return (
    <div className="space-y-3">
      <AgentResourcePickerField
        value={value.map((grant) => grant.id)}
        onChange={(ids) =>
          onChange(
            ids.map(
              (id) =>
                value.find((grant) => grant.id === id) ?? {
                  id,
                  maxLevel: OperationLevel.L1,
                  confirmL2: false,
                  outbound: false,
                },
            ),
          )
        }
        disabled={disabled}
        resourceKey={resourceKeys.agentBusinessSystemOptions()}
        load={() => listAgentBusinessSystemOptions()}
        toOptions={(systems) =>
          systems.map((system) => ({
            id: system.id,
            name: system.name,
            detail: t("businessSystems.tools", { count: system.toolCount }),
          }))
        }
        labels={{
          title: t("businessSystems.title"),
          group: t("businessSystems.label"),
          unconfigured: t("businessSystems.unconfigured"),
          selected: (count, selectedNames) =>
            selectedNames === ""
              ? t("businessSystems.selected", { count })
              : count === 1
                ? t("businessSystems.selectedOne", { names: selectedNames })
                : t("businessSystems.selectedNames", { names: selectedNames, count }),
          empty: t("businessSystems.empty"),
          loadError: t("businessSystems.loadError"),
        }}
      />
      {value.length > 0 ? (
        <div className="divide-y rounded-lg border">
          {value.map((grant) => {
            const name = names.get(grant.id) ?? grant.id
            /** 修改当前业务系统的授权。 */
            const update = (patch: Partial<AgentBusinessSystemGrant>) =>
              onChange(value.map((item) => (item.id === grant.id ? { ...item, ...patch } : item)))
            const confirmId = `agent-business-system-${grant.id}-confirm-l2`
            const outboundId = `agent-business-system-${grant.id}-outbound`
            return (
              <div key={grant.id} className="space-y-3 px-4 py-3">
                <div className="flex items-center gap-3">
                  <span className="min-w-0 flex-1 truncate text-sm" title={name}>
                    {name}
                  </span>
                  <NativeSelect
                    className="w-44 shrink-0"
                    aria-label={t("businessSystems.maxLevel", { name })}
                    disabled={disabled}
                    value={grant.maxLevel}
                    // 升到包含 L2 的级别时默认需要确认，降到 L2 以下时取消确认。
                    onChange={(event) => {
                      const maxLevel = event.target.value as OperationLevel
                      update({
                        maxLevel,
                        confirmL2: includesL2(maxLevel) && (includesL2(grant.maxLevel) ? grant.confirmL2 : true),
                      })
                    }}
                  >
                    {operationLevels.map((level) => (
                      <option key={level} value={level}>
                        {t(`integrations:businessSystem.levels.${level}`)}
                      </option>
                    ))}
                  </NativeSelect>
                </div>
                <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
                  <div className="flex items-center gap-2">
                    <Switch
                      id={confirmId}
                      checked={grant.confirmL2}
                      disabled={disabled || !includesL2(grant.maxLevel)}
                      onCheckedChange={(checked) => update({ confirmL2: checked })}
                    />
                    <Label htmlFor={confirmId} className="font-normal text-muted-foreground">
                      {t("businessSystems.confirmL2")}
                    </Label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Switch
                      id={outboundId}
                      checked={grant.outbound}
                      disabled={disabled}
                      onCheckedChange={(checked) => update({ outbound: checked })}
                    />
                    <Label htmlFor={outboundId} className="font-normal text-muted-foreground">
                      {t("businessSystems.outbound")}
                    </Label>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      ) : null}
    </div>
  )
}
