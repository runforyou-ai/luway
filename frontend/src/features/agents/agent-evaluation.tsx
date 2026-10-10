/** AI 员工详情的评测页签：发起评测、对比最近两次运行、维护评测用例，并在侧栏查看单条用例的回放结果。 */
import { useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  AgentEvaluationCaseSource,
  AgentEvaluationRunStatus,
  deleteAgentEvaluationCase,
  getAgentEvaluation,
  startAgentEvaluationRun,
  type AgentData,
  type AgentEvaluationAudienceId,
  type AgentEvaluationCase,
  type AgentEvaluation,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbar } from "@/components/list-toolbar"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"

import { AgentEvaluationCaseDialog, evaluationAudiencesOf } from "./agent-evaluation-case-dialog"
import { AgentEvaluationCaseSheet, EvaluationStatusBadge } from "./agent-evaluation-case-sheet"

/** 评测进行中刷新进度的间隔。 */
const runningRefreshMilliseconds = 3000

/** 展示 AI 员工的评测工具栏、用例列表与用例侧栏；caseId 是地址中打开的用例，onCaseChange 为空字符串时关闭侧栏。 */
export function AgentEvaluationPanel({
  agent,
  caseId,
  onCaseChange,
}: {
  agent: AgentData
  caseId: string
  onCaseChange: (caseId: string) => void
}) {
  const { t } = useTranslation(["agents", "common"])
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const [editing, setEditing] = useState<"create" | AgentEvaluationCase | null>(null)
  const [deleting, setDeleting] = useState<AgentEvaluationCase | null>(null)
  const evaluation = useResource(
    resourceKeys.agentEvaluation(agent.id),
    (signal) => getAgentEvaluation(agent.id, signal),
    {
      refetchInterval: (data) =>
        data?.latest?.status === AgentEvaluationRunStatus.Running ? runningRefreshMilliseconds : false,
    },
  )
  const data = evaluation.data
  const running = data?.latest?.status === AgentEvaluationRunStatus.Running
  const audiences = evaluationAudiencesOf(agent.serviceAudiences)

  /** 刷新评测页与用例详情。 */
  async function refresh() {
    await Promise.all([
      invalidate(resourceKeys.agentEvaluation(agent.id)),
      invalidate(resourceKeys.agentEvaluationCase(agent.id)),
    ])
  }

  const run = useMutation({
    mutationFn: async () => {
      await startAgentEvaluationRun(agent.id)
      await refresh()
    },
    onError: (error) => reportError(error, { log: "发起评测", fallback: t("evaluation.startError") }),
  })
  const starting = run.isPending
  const deletion = useMutation({
    mutationFn: (id: string) => deleteAgentEvaluationCase(agent.id, id),
    onSuccess: () => void refresh(),
    onError: (error) => reportError(error, { log: "删除评测用例", fallback: t("evaluation.deleteError") }),
  })

  /** 删除确认中的用例，打开的侧栏属于该用例时一并关闭。 */
  function remove() {
    if (!deleting) return
    const id = deleting.id
    deletion.mutate(id, {
      onSuccess: () => {
        if (caseId === id) onCaseChange("")
        setDeleting(null)
      },
    })
  }

  return (
    <>
      <ListToolbar>
        <Button
          type="button"
          size="sm"
          disabled={!data || running || starting || data.cases.length === 0 || !data.decisionModelReady}
          onClick={() => run.mutate()}
        >
          {running || starting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {running && data?.latest
            ? t("evaluation.running", { finished: data.latest.finished, total: data.latest.caseCount })
            : t("evaluation.start")}
        </Button>
        {data ? <EvaluationSummary evaluation={data} /> : null}
        <Button
          type="button"
          variant="subtle"
          size="icon-sm"
          className="ml-auto"
          aria-label={t("evaluation.createCase")}
          title={t("evaluation.createCase")}
          onClick={() => setEditing("create")}
        >
          <PlusIcon />
        </Button>
      </ListToolbar>
      <ResourceListLayout resources={evaluation} errorMessage={t("evaluation.loadError")}>
        <ResourceTable
          columns={[
            {
              key: "question",
              header: t("evaluation.form.question"),
              cellClassName: "w-full max-w-0",
              cell: (row) => (
                <div className="min-w-0">
                  <p className="truncate">{row.question}</p>
                  <p className="truncate text-xs text-muted-foreground">
                    {[
                      audiences.length > 1 ? t(`evaluation.audiences.${row.audience as AgentEvaluationAudienceId}`) : "",
                      row.source === AgentEvaluationCaseSource.Manual ? "" : t(`evaluation.sources.${row.source}`),
                      t(`evaluation.actions.${row.expectedAction}`),
                      row.expectedAnswer,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                </div>
              ),
            },
            {
              key: "result",
              header: t("evaluation.sheet.result"),
              cellClassName: "w-px whitespace-nowrap text-right",
              cell: (row) =>
                row.modified ? (
                  <StatusBadge variant="muted">{t("evaluation.modified")}</StatusBadge>
                ) : (
                  <span className="inline-flex items-center gap-2">
                    {row.newFailure ? <StatusBadge variant="warning">{t("evaluation.newFailure")}</StatusBadge> : null}
                    {row.latestStatus ? <EvaluationStatusBadge status={row.latestStatus} /> : null}
                    {row.rerunStatus ? (
                      <span className="text-xs text-muted-foreground">{t(`evaluation.rerunStatuses.${row.rerunStatus}`)}</span>
                    ) : null}
                  </span>
                ),
            },
          ]}
          rows={data?.cases ?? []}
          rowKey={(row) => row.id}
          empty={t("evaluation.noCases")}
          onRowActivate={(row) => onCaseChange(row.id)}
          rowActions={(row) => [
            { key: "edit", label: t("common:actions.edit"), onSelect: () => setEditing(row) },
            { key: "delete", label: t("common:actions.delete"), destructive: true, separatorBefore: true, onSelect: () => setDeleting(row) },
          ]}
        />
      </ResourceListLayout>
      <AgentEvaluationCaseSheet
        agentId={agent.id}
        caseId={caseId}
        running={running}
        onClose={() => onCaseChange("")}
        onEdit={setEditing}
      />
      <AgentEvaluationCaseDialog
        agentId={agent.id}
        audiences={audiences}
        editing={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null)
          void refresh()
        }}
      />
      <ConfirmationDialog
        open={deleting !== null}
        pending={deletion.isPending}
        title={t("evaluation.deleteTitle")}
        description={t("evaluation.deleteDescription")}
        onOpenChange={(open) => (open ? undefined : setDeleting(null))}
        onConfirm={remove}
      />
    </>
  )
}

/** 最近一次运行的结果与上次对比，评测进行中只显示上次结果，以及配置已变更或缺少判断模型的提示；通过率的分母不含评测异常。 */
function EvaluationSummary({ evaluation }: { evaluation: AgentEvaluation }) {
  const { t } = useTranslation("agents")
  const { latest, previous } = evaluation
  const parts: string[] = []
  if (latest?.status === AgentEvaluationRunStatus.Completed) {
    parts.push(t("evaluation.summary", { passed: latest.passed, total: latest.passed + latest.failed }))
    if (latest.errors > 0) parts.push(t("evaluation.summaryErrors", { count: latest.errors }))
  }
  if (previous?.status === AgentEvaluationRunStatus.Completed) {
    parts.push(t("evaluation.summaryPrevious", { passed: previous.passed, total: previous.passed + previous.failed }))
  }
  const hint = !evaluation.decisionModelReady
    ? t("evaluation.decisionModelRequired")
    : evaluation.configurationChanged && latest?.status === AgentEvaluationRunStatus.Completed
      ? t("evaluation.configurationChanged")
      : ""
  return (
    <p className="min-w-0 truncate text-sm text-muted-foreground">
      {[...parts, hint].filter(Boolean).join(" · ")}
    </p>
  )
}
