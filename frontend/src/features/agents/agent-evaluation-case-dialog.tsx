/** 新建或编辑 AI 员工评测用例的弹窗：填写提问人、提问、期望处理方式与标准答案。 */
import { useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { z } from "zod"

import {
  AgentRunOutcome,
  ServiceAudience,
  createAgentEvaluationCase,
  updateAgentEvaluationCase,
  type AgentEvaluationAudienceId,
  type AgentEvaluationCase,
} from "@/api"
import { AutoGrowTextarea } from "@/components/form/auto-grow-textarea"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { zodResolver } from "@/lib/zod-resolver"

/** 期望处理方式的展示顺序。 */
const evaluationActions: readonly AgentRunOutcome[] = [
  AgentRunOutcome.Reply,
  AgentRunOutcome.AskCustomer,
  AgentRunOutcome.Resolve,
  AgentRunOutcome.Handoff,
]

/** 评测用例可选的提问人服务对象。 */
const evaluationAudiences: readonly AgentEvaluationAudienceId[] = [ServiceAudience.Customer, ServiceAudience.Employee]

/** 返回 AI 员工服务对象中可作为评测提问人的部分。 */
export function evaluationAudiencesOf(audiences: readonly ServiceAudience[]) {
  return evaluationAudiences.filter((audience) => audiences.includes(audience))
}

/** 按 editing 打开弹窗：为 "create" 时新建，为用例时编辑该用例，为 null 时关闭；audiences 是 AI 员工的服务对象，多于一个时显示提问人。 */
export function AgentEvaluationCaseDialog({
  agentId,
  audiences,
  editing,
  onClose,
  onSaved,
}: {
  agentId: string
  audiences: AgentEvaluationAudienceId[]
  editing: "create" | AgentEvaluationCase | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation("agents")
  return (
    <Dialog open={editing !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(editing === "create" ? "evaluation.form.createTitle" : "evaluation.form.editTitle")}</DialogTitle>
        </DialogHeader>
        {editing !== null ? (
          <AgentEvaluationCaseForm
            key={editing === "create" ? "create" : editing.id}
            agentId={agentId}
            audiences={audiences}
            editing={editing === "create" ? null : editing}
            onCancel={onClose}
            onSaved={onSaved}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

/** 评测用例表单，期望转人工时不填标准答案。 */
function AgentEvaluationCaseForm({
  agentId,
  audiences,
  editing,
  onCancel,
  onSaved,
}: {
  agentId: string
  audiences: AgentEvaluationAudienceId[]
  editing: AgentEvaluationCase | null
  onCancel: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation(["agents", "common"])
  const reportError = useRequestErrorReporter()
  const schema = useMemo(
    () =>
      z
        .object({
          audience: z.enum([ServiceAudience.Customer, ServiceAudience.Employee]),
          question: z.string().trim().min(1),
          expectedAction: z.enum([
            AgentRunOutcome.Reply,
            AgentRunOutcome.AskCustomer,
            AgentRunOutcome.Resolve,
            AgentRunOutcome.Handoff,
          ]),
          expectedAnswer: z.string(),
        })
        .refine(
          (values) => values.expectedAction === AgentRunOutcome.Handoff || values.expectedAnswer.trim() !== "",
          { path: ["expectedAnswer"], message: t("evaluation.form.expectedAnswerRequired") },
        ),
    [t],
  )
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      audience: (editing?.audience as AgentEvaluationAudienceId | undefined) ?? audiences[0],
      question: editing?.question ?? "",
      expectedAction: editing?.expectedAction ?? AgentRunOutcome.Reply,
      expectedAnswer: editing?.expectedAnswer ?? "",
    },
  })
  const expectedAction = useWatch({ control: form.control, name: "expectedAction" })
  const needsAnswer = expectedAction !== AgentRunOutcome.Handoff

  /** 保存用例，期望转人工时不提交标准答案。 */
  async function submit(values: z.infer<typeof schema>) {
    const input = { ...values, expectedAnswer: needsAnswer ? values.expectedAnswer : "" }
    try {
      if (editing) await updateAgentEvaluationCase(agentId, editing.id, input)
      else await createAgentEvaluationCase(agentId, input)
      onSaved()
    } catch (error) {
      reportError(error, {
        log: "保存评测用例",
        fields: ["audience", "question", "expectedAction", "expectedAnswer"],
      })
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        {audiences.length > 1 ? (
          <Controller
            name="audience"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor="evaluation-audience" required>
                  {t("evaluation.form.audience")}
                </FieldLabel>
                <NativeSelect {...field} id="evaluation-audience" aria-invalid={fieldState.invalid}>
                  {audiences.map((audience) => (
                    <option key={audience} value={audience}>
                      {t(`evaluation.audiences.${audience}`)}
                    </option>
                  ))}
                </NativeSelect>
              </Field>
            )}
          />
        ) : null}
        <Controller
          name="question"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="evaluation-question" required>
                {t("evaluation.form.question")}
              </FieldLabel>
              <AutoGrowTextarea {...field} id="evaluation-question" required autoFocus aria-invalid={fieldState.invalid} />
            </Field>
          )}
        />
        <Controller
          name="expectedAction"
          control={form.control}
          rules={{ deps: ["expectedAnswer"] }}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="evaluation-expected-action" required>
                {t("evaluation.form.expectedAction")}
              </FieldLabel>
              <NativeSelect {...field} id="evaluation-expected-action" aria-invalid={fieldState.invalid}>
                {evaluationActions.map((action) => (
                  <option key={action} value={action}>
                    {t(`evaluation.actions.${action}`)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        />
        {needsAnswer ? (
          <Controller
            name="expectedAnswer"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor="evaluation-expected-answer" required>
                  {t("evaluation.form.expectedAnswer")}
                </FieldLabel>
                <AutoGrowTextarea {...field} id="evaluation-expected-answer" required aria-invalid={fieldState.invalid} />
                <FieldDescription>{t("evaluation.form.expectedAnswerHelp")}</FieldDescription>
              </Field>
            )}
          />
        ) : null}
      </FieldGroup>
      <div className="flex items-center justify-end gap-2">
        <Button type="button" variant="outline" disabled={isSubmitting} onClick={onCancel}>
          {t("common:actions.cancel")}
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("common:actions.saving") : t("common:actions.save")}
        </Button>
      </div>
    </form>
  )
}
