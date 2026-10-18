/** AI 员工对话模型选择字段；平台部署隐藏选择器，存量引用可切到 Auto。 */
import {
  Controller,
  type Control,
  type FieldPathByValue,
  type FieldValues,
} from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"

import { AIModelUsage } from "@/api"
import { AIModelOptionGroups, useAIModelOptions } from "@/components/ai-model-options"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { platformModelRef, useModelSource } from "@/hooks/use-model-source"
import { resolveAppPlatform } from "@/platform/app-platform"

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
  const { t } = useTranslation(["agents", "common"])
  const { platformOnly } = useModelSource()
  const modelsResource = useAIModelOptions(AIModelUsage.Agent)
  const models = modelsResource.data ?? []
  const loading = modelsResource.loading
  const failed = Boolean(modelsResource.error)

  if (platformOnly) {
    return (
      <Controller
        name={name}
        control={control}
        render={({ field, fieldState }) => {
          const value = field.value ?? ""
          // 已是平台 Auto 或空值时不展示；存量工作区引用保留切到 Auto 的恢复路径，直至迁移完成。
          if (value === "" || value === platformModelRef) return <></>
          return (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={`${name}-select`} required>
                {t("execution.model")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id={`${name}-select`}
                required
                disabled={disabled}
                aria-label={t("execution.model")}
                aria-invalid={fieldState.invalid}
              >
                <option value={value}>{t("execution.legacyWorkspaceModel")}</option>
                <option value={platformModelRef}>{t("common:aiModels.platformGroup")}</option>
              </NativeSelect>
              <FieldDescription>{t("execution.legacyWorkspaceModelHelp")}</FieldDescription>
            </Field>
          )
        }}
      />
    )
  }

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
            <AIModelOptionGroups models={models} />
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
