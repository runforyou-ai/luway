/** AI 员工运行配置表单。 */
import { useEffect, useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"

import { updateAgentExecution, type AgentData } from "@/api"
import { AgentBehaviorSummary } from "@/components/agent-behavior-summary"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { AgentKnowledgeField } from "@/components/agent-fields/agent-knowledge-field"
import { AgentBusinessSystemsField } from "@/components/agent-fields/agent-business-systems-field"
import { AgentModelField } from "@/components/agent-fields/agent-model-field"
import {
  createAgentExecutionSchema,
  type AgentExecutionFormValues,
} from "@/features/agents/agent-schema"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { zodResolver } from "@/lib/zod-resolver"

/** 整体保存模型、指令、知识库和业务系统授权。 */
export function AgentExecutionForm({
  agent,
  onSaved,
}: {
  agent: AgentData
  onSaved: () => void
}) {
  const { t } = useTranslation(["agents", "common"])
  const reportError = useRequestErrorReporter()
  const schema = useMemo(
    () =>
      createAgentExecutionSchema({
        instructionTooLong: t("validation.instructionTooLong"),
      }),
    [t],
  )
  const managed = agent.execution.managed
  const form = useForm<AgentExecutionFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      modelId: managed.model.id,
      systemInstruction: managed.systemInstruction,
      knowledgeBaseIds: managed.knowledgeBaseIds,
      businessSystems: agent.execution.businessSystems,
    },
  })
  const { mounted, dirty, discarded } = useFormLifetime(form.formState.isDirty)

  // 外部删除业务系统后只同步未编辑的授权，保留其他表单草稿。
  useEffect(() => {
    if (!form.getFieldState("businessSystems").isDirty) {
      form.resetField("businessSystems", { defaultValue: agent.execution.businessSystems })
    }
  }, [agent.execution.businessSystems, form])

  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save: submit, discarded })

  /** 提交当前运行配置并生成一个生效版本。 */
  async function submit(values: AgentExecutionFormValues) {
    try {
      const saved = await updateAgentExecution(agent.id, {
        mode: agent.execution.mode,
        businessSystems: values.businessSystems,
        managed: {
          modelId: values.modelId,
          systemInstruction: values.systemInstruction,
          knowledgeBaseIds: values.knowledgeBaseIds,
        },
      })
      onSaved()
      if (!mounted.current) return true
      const next = { ...values, businessSystems: saved.execution.businessSystems }
      dirty.current = !acceptSaved(values, next)
      return true
    } catch (error) {
      // 离开页面后提交的改动失败时同样提示。
      reportError(error, {
        log: "保存 AI 员工运行配置",
        context: { agent_id: agent.id },
        fallback: t("execution.saveError"),
        fields: ["execution", "modelId", "systemInstruction", "knowledgeBaseIds", "businessSystems"],
      })
      return false
    }
  }

  return (
    <form onSubmit={form.handleSubmit(() => saveNow())} noValidate>
      <FieldGroup>
        <AgentModelField
          control={form.control}
          name="modelId"
          disabled={form.formState.isSubmitting}
        />
        <Controller
          name="systemInstruction"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="agent-execution-instruction">
                {t("execution.instruction")}
              </FieldLabel>
              <Textarea
                {...field}
                id="agent-execution-instruction"
                rows={10}
                aria-invalid={fieldState.invalid}
                disabled={form.formState.isSubmitting}
              />
              <FieldDescription>
                {t("execution.instructionHelp")}
              </FieldDescription>
            </Field>
          )}
        />
        <Field>
          <FieldLabel>{t("execution.behavior")}</FieldLabel>
          <AgentBehaviorSummary behavior={agent.behavior} />
        </Field>
        <Controller
          name="knowledgeBaseIds"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel>{t("execution.knowledgeBases")}</FieldLabel>
              <AgentKnowledgeField
                value={field.value}
                onChange={field.onChange}
                disabled={form.formState.isSubmitting}
              />
            </Field>
          )}
        />
        <Controller
          name="businessSystems"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel>{t("businessSystems.label")}</FieldLabel>
              <FieldDescription>{t("businessSystems.help")}</FieldDescription>
              <AgentBusinessSystemsField value={field.value} onChange={field.onChange} disabled={form.formState.isSubmitting} />
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}
