/** 业务系统工具设置侧栏：事实修正、停用与参数绑定，每项改动立即保存。 */
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import {
  contextValues,
  updateBusinessToolSetting,
  type BusinessTool,
  type BusinessToolSettingInput,
  type ContextValue,
} from "@/api"
import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Switch } from "@/components/ui/switch"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 工具的三项事实及其输入字段。 */
const facts = ["readOnly", "reversible", "outbound"] as const
type Fact = (typeof facts)[number]

/** tool 为空时关闭侧栏；改动以工具当前设置为基础整体提交，保存期间禁用控件。 */
export function BusinessToolSheet({
  systemId,
  tool,
  onClose,
}: {
  systemId: string
  tool: BusinessTool | null
  onClose: () => void
}) {
  const { t } = useTranslation("integrations")
  const reportError = useRequestErrorReporter()
  const invalidateResource = useResourceInvalidator()
  const setting = useMutation({
    mutationFn: async (input: BusinessToolSettingInput) => {
      await updateBusinessToolSetting(systemId, input)
      void invalidateResource(resourceKeys.businessSystems())
      await invalidateResource(resourceKeys.businessSystem(systemId))
    },
    onError: (error) => reportError(error, { fallback: t("businessSystem.toolSetting.saveError") }),
  })
  const saving = setting.isPending

  /** 以工具当前设置合并本次改动并保存，事实与默认一致时不保存修正值。 */
  function save(change: Partial<Omit<BusinessToolSettingInput, "toolName">>) {
    if (!tool || saving) return
    const input: BusinessToolSettingInput = {
      toolName: tool.name,
      readOnly: tool.readOnly,
      reversible: tool.reversible,
      outbound: tool.outbound,
      disabled: tool.disabled,
      parameterBindings: tool.parameterBindings,
      ...change,
    }
    for (const fact of facts) {
      if (input[fact] === tool.defaultFacts[fact]) input[fact] = null
    }
    setting.mutate(input)
  }

  /** 修改一个参数的绑定，选择由 AI 员工填写时移除该绑定。 */
  function bindParameter(parameter: string, value: ContextValue | "") {
    if (!tool) return
    const others = tool.parameterBindings.filter((binding) => binding.parameter !== parameter)
    save({ parameterBindings: value ? [...others, { parameter, value }] : others })
  }

  const overridden = tool ? facts.some((fact) => tool[fact] !== null) : false

  return (
    <Sheet open={tool !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent className="w-full gap-0 p-0 sm:max-w-lg">
        <SheetHeader className="border-b px-6 py-4 pr-12">
          <SheetTitle className="font-mono">{tool?.name}</SheetTitle>
          <SheetDescription>{tool?.description || t("businessSystem.tools.noDescription")}</SheetDescription>
        </SheetHeader>
        {tool ? (
          <ScrollArea className="min-h-0 flex-1">
            <FieldGroup className="p-6">
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel>{t("businessSystem.toolSetting.level")}</FieldLabel>
                </FieldContent>
                <span className="text-sm font-medium">{t(`businessSystem.levels.${tool.level}`)}</span>
              </Field>
              <div className="space-y-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="text-sm font-medium">{t("businessSystem.toolSetting.facts")}</div>
                  {overridden ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={saving}
                      onClick={() => save({ readOnly: null, reversible: null, outbound: null })}
                    >
                      {t("businessSystem.toolSetting.resetFacts")}
                    </Button>
                  ) : null}
                </div>
                {facts.map((fact: Fact) => (
                  <Field key={fact} orientation="horizontal">
                    <FieldContent>
                      <FieldLabel htmlFor={`business-tool-${fact}`}>{t(`businessSystem.toolSetting.${fact}`)}</FieldLabel>
                      <FieldDescription>{t(`businessSystem.toolSetting.${fact}Help`)}</FieldDescription>
                    </FieldContent>
                    <Switch
                      id={`business-tool-${fact}`}
                      checked={tool.facts[fact]}
                      // 只读工具不区分是否可撤销。
                      disabled={saving || (fact === "reversible" && tool.facts.readOnly)}
                      onCheckedChange={(checked) => save({ [fact]: checked })}
                    />
                  </Field>
                ))}
              </div>
              <div className="space-y-4">
                <div>
                  <div className="text-sm font-medium">{t("businessSystem.toolSetting.parameterBindings")}</div>
                  <FieldDescription className="mt-1">{t("businessSystem.toolSetting.parameterBindingsHelp")}</FieldDescription>
                </div>
                {tool.parameters.length ? (
                  <div className="divide-y rounded-lg border">
                    {tool.parameters.map((parameter) => (
                      <div key={parameter} className="flex items-center gap-3 px-4 py-3">
                        <span className="min-w-0 flex-1 truncate font-mono text-sm" title={parameter}>
                          {parameter}
                        </span>
                        <NativeSelect
                          className="w-44 shrink-0"
                          aria-label={parameter}
                          disabled={saving}
                          value={tool.parameterBindings.find((binding) => binding.parameter === parameter)?.value ?? ""}
                          onChange={(event) => bindParameter(parameter, event.target.value as ContextValue | "")}
                        >
                          <option value="">{t("businessSystem.toolSetting.notBound")}</option>
                          {contextValues.map((value) => (
                            <option key={value} value={value}>
                              {t(`businessSystem.contextValues.${value}`)}
                            </option>
                          ))}
                        </NativeSelect>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-sm text-muted-foreground">{t("businessSystem.toolSetting.noParameters")}</p>
                )}
              </div>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldLabel htmlFor="business-tool-disabled">{t("businessSystem.toolSetting.disabled")}</FieldLabel>
                  <FieldDescription>{t("businessSystem.toolSetting.disabledHelp")}</FieldDescription>
                </FieldContent>
                <Switch
                  id="business-tool-disabled"
                  checked={tool.disabled}
                  disabled={saving}
                  onCheckedChange={(checked) => save({ disabled: checked })}
                />
              </Field>
            </FieldGroup>
          </ScrollArea>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
