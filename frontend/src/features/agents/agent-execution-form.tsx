/** AI 员工运行配置表单。 */
import { useEffect, useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError, updateAgentExecution, type AgentData } from "@/api"
import { AgentBehaviorSummary } from "@/components/agent-behavior-summary"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { AgentKnowledgeField } from "@/components/agent-fields/agent-knowledge-field"
import { AgentMCPField } from "@/components/agent-fields/agent-mcp-field"
import { AgentModelField } from "@/components/agent-fields/agent-model-field"
import {
  agentModelSelection,
  parseAgentModelSelection,
} from "@/lib/agent-model-selection"
import {
  createAgentExecutionSchema,
  type AgentExecutionFormValues,
} from "@/features/agents/agent-schema"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { apiErrorMessage } from "@/lib/form-errors"
import { useAutoSave } from "@/hooks/use-auto-save"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 整体保存模型、指令、知识库和 MCP 服务绑定。 */
export function AgentExecutionForm({
  agent,
  onSaved,
}: {
  agent: AgentData
  onSaved: () => void
}) {
  const { t } = useTranslation(["agents", "common"])
  const navigate = useNavigate()
  const schema = useMemo(
    () =>
      createAgentExecutionSchema({
        modelRequired: t("validation.modelRequired"),
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
      modelSelection: agentModelSelection(
        managed.providerId,
        managed.modelIdentifier,
      ),
      systemInstruction: managed.systemInstruction,
      knowledgeBaseIds: managed.knowledgeBaseIds,
      mcpServerIds: agent.execution.mcpServerIds,
    },
  })
  const { mounted, dirty, discarded } = useFormLifetime(form.formState.isDirty)

  // 外部删除服务后只同步未编辑的绑定，保留其他表单草稿。
  useEffect(() => {
    if (!form.getFieldState("mcpServerIds").isDirty) {
      form.resetField("mcpServerIds", { defaultValue: agent.execution.mcpServerIds })
    }
  }, [agent.execution.mcpServerIds, form])

  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save: submit, discarded })

  /** 提交当前运行配置并生成一个生效版本。 */
  async function submit(values: AgentExecutionFormValues) {
    try {
      const saved = await updateAgentExecution(agent.id, {
        mode: agent.execution.mode,
        mcpServerIds: values.mcpServerIds,
        managed: {
          ...parseAgentModelSelection(values.modelSelection),
          systemInstruction: values.systemInstruction,
          knowledgeBaseIds: values.knowledgeBaseIds,
        },
      })
      onSaved()
      if (!mounted.current) return true
      const next = { ...values, mcpServerIds: saved.execution.mcpServerIds }
      dirty.current = !acceptSaved(values, next)
      return true
    } catch (error) {
      // 离开页面后提交的改动失败时同样提示。
      if (recoverSession(error, navigate)) return false
      console.warn("保存 AI 员工运行配置失败", { agent_id: agent.id, error })
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, [
              "execution",
              "providerId",
              "modelIdentifier",
              "systemInstruction",
              "knowledgeBaseIds",
              "mcpServerIds",
            ])
          : t("execution.saveError"),
      )
      return false
    }
  }

  return (
    <form onSubmit={form.handleSubmit(() => saveNow())} noValidate>
      <FieldGroup>
        <AgentModelField
          control={form.control}
          name="modelSelection"
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
          name="mcpServerIds"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel>{t("mcp.services")}</FieldLabel>
              <AgentMCPField value={field.value} onChange={field.onChange} disabled={form.formState.isSubmitting} />
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}
