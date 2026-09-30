/** AI 员工对话模型选择字段。 */
import { useMemo } from "react"
import {
  Controller,
  type Control,
  type FieldPathByValue,
  type FieldValues,
} from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"

import { listAgentModelOptions, type AgentModelOption } from "@/api"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { agentModelSelection } from "@/lib/agent-model-selection"
import { resolveAppPlatform } from "@/platform/app-platform"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 按供应商分组模型。 */
function groupModels(models: AgentModelOption[]) {
  const groups = new Map<
    string,
    { providerName: string; models: AgentModelOption[] }
  >()
  for (const model of models) {
    const group = groups.get(model.providerId)
    if (group) {
      group.models.push(model)
    } else {
      groups.set(model.providerId, {
        providerName: model.providerName,
        models: [model],
      })
    }
  }
  return [...groups.entries()]
}

/** 渲染 AI 员工对话模型选择字段。 */
export function AgentModelField<TValues extends FieldValues>({
  control,
  name,
  disabled = false,
}: {
  control: Control<TValues>
  name: FieldPathByValue<TValues, string>
  disabled?: boolean
}) {
  const { t } = useTranslation("agents")
  const modelsResource = useResource(
    resourceKeys.agentModelOptions(),
    () => listAgentModelOptions(),
    { staleTime: 0 },
  )
  const models = modelsResource.data ?? []
  const loading = modelsResource.loading
  const failed = Boolean(modelsResource.error)
  const groups = useMemo(() => groupModels(models), [models])

  return (
    <Controller
      name={name}
      control={control}
      render={({ field, fieldState }) => (
        <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor={`${name}-select`} required>
            {t("execution.model")}
          </FieldLabel>
          <NativeSelect
            {...field}
            id={`${name}-select`}
            required
            disabled={disabled || loading}
            aria-label={t("execution.model")}
            aria-invalid={fieldState.invalid}
          >
            <option value="">
              {loading
                ? t("execution.modelLoading")
                : t("execution.modelSelect")}
            </option>
            {groups.map(([providerId, group]) => (
              <optgroup key={providerId} label={group.providerName}>
                {group.models.map((model) => (
                  <option
                    key={model.modelIdentifier}
                    value={agentModelSelection(
                      model.providerId,
                      model.modelIdentifier,
                    )}
                  >
                    {model.modelName}
                  </option>
                ))}
              </optgroup>
            ))}
          </NativeSelect>
          {failed ? (
            <FieldDescription>
              {t("execution.modelLoadError")}
            </FieldDescription>
          ) : !loading && models.length === 0 ? (
            <FieldDescription>
              {t("execution.noModels")}{" "}
              {/* 模型服务只在 Web 与桌面端配置。 */}
              {resolveAppPlatform() === "mobile" ? (
                t("execution.configureModelsOnDesktop")
              ) : (
                <Link to="/settings/model-services">
                  {t("execution.configureModels")}
                </Link>
              )}
            </FieldDescription>
          ) : null}
        </Field>
      )}
    />
  )
}
