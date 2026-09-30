/** AI 员工详情的评测页签：发起评测、对比最近两次运行、维护评测用例，并在侧栏查看单条用例的回放结果。 */
import { useState } from "react"
import { LoaderCircleIcon, PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  AgentEvaluationCaseSource,
  AgentEvaluationRunStatus,
  deleteAgentEvaluationCase,
  getAgentEvaluation,
  isApiError,
  startAgentEvaluationRun,
  type AgentData,
  type AgentEvaluationCaseData,
  type AgentEvaluationData,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbar } from "@/components/list-toolbar"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

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
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [editing, setEditing] = useState<"create" | AgentEvaluationCaseData | null>(null)
  const [deleting, setDeleting] = useState<AgentEvaluationCaseData | null>(null)
  const [deletePending, setDeletePending] = useState(false)
  const [starting, setStarting] = useState(false)
  const evaluation = useResource(
    resourceKeys.agentEvaluation(agent.id),
    (signal) => getAgentEvaluation(agent.id, signal),
    {
      refetchInterval: (data) =>
        data?.latest?.status === AgentEvaluationRunStatus.AgentEvaluationRunStatusRunning ? runningRefreshMilliseconds : false,
    },
  )
  const data = evaluation.data
  const running = data?.latest?.status === AgentEvaluationRunStatus.AgentEvaluationRunStatusRunning
  const audiences = evaluationAudiencesOf(agent.serviceAudiences)

  /** 刷新评测页与用例详情。 */
  async function refresh() {
    await Promise.all([
      invalidate(resourceKeys.agentEvaluation(agent.id)),
      invalidate(resourceKeys.agentEvaluationCase(agent.id)),
    ])
  }

  /** 发起一次评测运行。 */
  async function start() {
    setStarting(true)
    try {
      await startAgentEvaluationRun(agent.id)
      await refresh()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("发起评测失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("evaluation.startError"))
    } finally {
      setStarting(false)
    }
  }

  /** 删除确认中的用例，打开的侧栏属于该用例时一并关闭。 */
  async function remove() {
    if (!deleting) return
    setDeletePending(true)
    try {
      await deleteAgentEvaluationCase(agent.id, deleting.id)
      if (caseId === deleting.id) onCaseChange("")
      setDeleting(null)
      await refresh()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("删除评测用例失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("evaluation.deleteError"))
    } finally {
      setDeletePending(false)
    }
  }

  return (
    <>
      <ListToolbar>
        <Button
          type="button"
          size="sm"
          disabled={!data || running || starting || data.cases.length === 0 || !data.decisionModelReady}
          onClick={() => void start()}
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
                      audiences.length > 1 ? t(`evaluation.audiences.${row.audience}`) : "",
                      row.source === AgentEvaluationCaseSource.AgentEvaluationCaseSourceManual ? "" : t(`evaluation.sources.${row.source}`),
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
        pending={deletePending}
        title={t("evaluation.deleteTitle")}
        description={t("evaluation.deleteDescription")}
        onOpenChange={(open) => (open ? undefined : setDeleting(null))}
        onConfirm={() => void remove()}
      />
    </>
  )
}

/** 最近一次运行的结果与上次对比，评测进行中只显示上次结果，以及配置已变更或缺少判断模型的提示；通过率的分母不含评测异常。 */
function EvaluationSummary({ evaluation }: { evaluation: AgentEvaluationData }) {
  const { t } = useTranslation("agents")
  const { latest, previous } = evaluation
  const parts: string[] = []
  if (latest?.status === AgentEvaluationRunStatus.AgentEvaluationRunStatusCompleted) {
    parts.push(t("evaluation.summary", { passed: latest.passed, total: latest.passed + latest.failed }))
    if (latest.errors > 0) parts.push(t("evaluation.summaryErrors", { count: latest.errors }))
  }
  if (previous?.status === AgentEvaluationRunStatus.AgentEvaluationRunStatusCompleted) {
    parts.push(t("evaluation.summaryPrevious", { passed: previous.passed, total: previous.passed + previous.failed }))
  }
  const hint = !evaluation.decisionModelReady
    ? t("evaluation.decisionModelRequired")
    : evaluation.configurationChanged && latest?.status === AgentEvaluationRunStatus.AgentEvaluationRunStatusCompleted
      ? t("evaluation.configurationChanged")
      : ""
  return (
    <p className="min-w-0 truncate text-sm text-muted-foreground">
      {[...parts, hint].filter(Boolean).join(" · ")}
    </p>
  )
}
