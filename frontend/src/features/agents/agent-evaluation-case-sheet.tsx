/** AI 员工评测用例侧栏：展示提问、期望与最近一次运行中每次尝试的结果和运行过程，并提供重新运行与编辑入口。 */
import { useState } from "react"
import { ChevronDownIcon, LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  AgentEvaluationResultStatus,
  AgentRunOutcome,
  getAgentEvaluationCase,
  isApiError,
  rerunAgentEvaluationCase,
  type AgentEvaluationAttemptData,
  type AgentEvaluationContextMessageData,
  type AgentEvaluationResultStatusId,
  type AgentEvaluationCaseData,
} from "@/api"
import { ResourceContent } from "@/components/resource-content"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { AgentRunBlocks } from "@/components/agent-run-blocks"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { handoffReasonKey } from "@/lib/handoff-reason-labels"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 进行中的尝试刷新结果的间隔。 */
const pendingRefreshMilliseconds = 3000

/** 按用例编号打开侧栏，caseId 为空时关闭；running 表示有进行中的评测运行，此时不能重新运行。 */
export function AgentEvaluationCaseSheet({
  agentId,
  caseId,
  running,
  onClose,
  onEdit,
}: {
  agentId: string
  caseId: string
  running: boolean
  onClose: () => void
  onEdit: (evaluationCase: AgentEvaluationCaseData) => void
}) {
  const { t } = useTranslation(["agents", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [rerunning, setRerunning] = useState(false)
  const detail = useResource(
    resourceKeys.agentEvaluationCase(agentId, caseId),
    (signal) => getAgentEvaluationCase(agentId, caseId, signal),
    {
      enabled: Boolean(caseId),
      refetchInterval: (data) =>
        data?.attempts.some((attempt) => attempt.status === AgentEvaluationResultStatus.AgentEvaluationResultStatusPending)
          ? pendingRefreshMilliseconds
          : false,
    },
  )
  const data = detail.data?.case.id === caseId ? detail.data : undefined
  const pending = data?.attempts.some((attempt) => attempt.status === AgentEvaluationResultStatus.AgentEvaluationResultStatusPending) ?? false
  // 用例在最近一次运行后修改过时，下面的结果按修改前的内容判定，不能重跑。
  const modified = Boolean(data && data.attempts.length > 0 && data.attempts[0].caseVersion !== data.case.version)

  /** 在最近一次运行中重新运行该用例。 */
  async function rerun() {
    setRerunning(true)
    try {
      await rerunAgentEvaluationCase(agentId, caseId)
      await invalidate(resourceKeys.agentEvaluationCase(agentId, caseId))
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("重新运行评测用例失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("evaluation.sheet.rerunError"))
    } finally {
      setRerunning(false)
    }
  }

  return (
    <Sheet open={Boolean(caseId)} onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent className="w-full gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b px-6 py-4 pr-12">
          <SheetTitle>{t("evaluation.sheet.title")}</SheetTitle>
          <SheetDescription>{data?.case.question ?? null}</SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div className="p-6">
            <ResourceContent resources={detail} errorMessage={t("evaluation.sheet.loadError")}>
              {data ? (
                <div className="space-y-9">
                  <section className="space-y-4 text-sm">
                    {data.case.messages.length > 0 ? <EvaluationContext messages={data.case.messages} /> : null}
                    <LabeledText label={t("evaluation.sheet.question")} text={data.case.question} />
                    <LabeledText label={t("evaluation.sheet.expected")} text={t(`evaluation.actions.${data.case.expectedAction}`)} />
                    {data.case.expectedAction !== AgentRunOutcome.AgentRunOutcomeHandoff ? (
                      <LabeledText label={t("evaluation.sheet.expectedAnswer")} text={data.case.expectedAnswer} />
                    ) : null}
                    <div className="flex gap-2">
                      <Button type="button" variant="outline" size="sm" onClick={() => onEdit(data.case)}>
                        {t("common:actions.edit")}
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={running || pending || rerunning || modified || data.attempts.length === 0}
                        onClick={() => void rerun()}
                      >
                        {rerunning ? <LoaderCircleIcon className="animate-spin" /> : null}
                        {t("evaluation.sheet.rerun")}
                      </Button>
                    </div>
                    {modified ? <p className="text-muted-foreground">{t("evaluation.sheet.modified")}</p> : null}
                  </section>
                  {data.attempts.length === 0 ? (
                    <p className="text-sm text-muted-foreground">{t("evaluation.sheet.notRun")}</p>
                  ) : (
                    data.attempts.map((attempt) => <EvaluationAttempt key={attempt.id} attempt={attempt} modified={modified} />)
                  )}
                </div>
              ) : null}
            </ResourceContent>
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}

/** 按发送方列出用例前文。 */
function EvaluationContext({ messages }: { messages: AgentEvaluationContextMessageData[] }) {
  const { t } = useTranslation("agents")
  return (
    <div className="space-y-1">
      <p className="text-xs text-muted-foreground">{t("evaluation.sheet.context")}</p>
      <div className="max-h-72 space-y-3 overflow-y-auto rounded-lg border p-3">
        {messages.map((message, index) => (
          <div key={index} className="space-y-0.5 px-2 py-1">
            <p className="text-xs text-muted-foreground">{t(`evaluation.senders.${message.sender}`)}</p>
            <p className="break-words whitespace-pre-wrap">{message.body}</p>
          </div>
        ))}
      </div>
    </div>
  )
}

/** 带标题的多行文本。 */
function LabeledText({ label, text }: { label: string; text: string }) {
  return (
    <div className="space-y-1">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="whitespace-pre-wrap break-words">{text}</p>
    </div>
  )
}

/** 展示一次尝试的结果：用例已修改时先列出评测时的提问与期望，再展示评测异常的原因、转人工原因或 AI 回复，以及可展开的运行过程。 */
function EvaluationAttempt({ attempt, modified }: { attempt: AgentEvaluationAttemptData; modified: boolean }) {
  const { t } = useTranslation(["agents", "inbox"])
  return (
    <section className="space-y-3 text-sm">
      <div className="flex items-center gap-2">
        <h3 className="font-medium">
          {attempt.attempt === 1 ? t("evaluation.sheet.result") : t("evaluation.sheet.rerunResult", { attempt: attempt.attempt - 1 })}
        </h3>
        <EvaluationStatusBadge status={attempt.status} />
      </div>
      {modified ? (
        <div className="space-y-2 rounded-md bg-muted px-3 py-2">
          <LabeledText label={t("evaluation.sheet.evaluatedQuestion")} text={attempt.snapshot.question} />
          <LabeledText
            label={t("evaluation.sheet.evaluatedExpected")}
            text={[t(`evaluation.actions.${attempt.snapshot.expectedAction}`), attempt.snapshot.expectedAnswer].filter(Boolean).join(" · ")}
          />
        </div>
      ) : null}
      {attempt.errorCode ? (
        <p className="text-muted-foreground">{t(`evaluation.errors.${attempt.errorCode}`)}</p>
      ) : null}
      {attempt.actualAction === AgentRunOutcome.AgentRunOutcomeHandoff ? (
        <p className="text-muted-foreground">
          {t("evaluation.sheet.handoff", { reason: t(`inbox:${handoffReasonKey(attempt.actualReason)}`) })}
        </p>
      ) : null}
      {attempt.answer ? (
        <div className="space-y-1">
          <p className="text-xs text-muted-foreground">
            {attempt.actualAction ? t(`evaluation.actions.${attempt.actualAction}`) : t("evaluation.sheet.reply")}
          </p>
          <p className="whitespace-pre-wrap break-words">{attempt.answer}</p>
        </div>
      ) : null}
      {attempt.blocks.length > 0 ? (
        <Collapsible>
          <CollapsibleTrigger className="group flex items-center gap-1.5 rounded-sm text-xs text-muted-foreground focus-visible:outline focus-visible:outline-ring">
            {t("evaluation.sheet.process")}
            <ChevronDownIcon aria-hidden className="size-3.5 transition-transform group-data-[state=open]:rotate-180" />
          </CollapsibleTrigger>
          <CollapsibleContent className="mt-2 space-y-3 border-l pl-3">
            <AgentRunBlocks blocks={attempt.blocks} thinkingClassName="text-muted-foreground" />
          </CollapsibleContent>
        </Collapsible>
      ) : null}
    </section>
  )
}

/** 按评测结果显示状态标签，进行中的尝试显示中性标签。 */
export function EvaluationStatusBadge({ status }: { status: AgentEvaluationResultStatusId }) {
  const { t } = useTranslation("agents")
  const variant =
    status === AgentEvaluationResultStatus.AgentEvaluationResultStatusPassed
      ? "success"
      : status === AgentEvaluationResultStatus.AgentEvaluationResultStatusFailed
        ? "destructive"
        : "muted"
  return <StatusBadge variant={variant}>{t(`evaluation.statuses.${status}`)}</StatusBadge>
}
