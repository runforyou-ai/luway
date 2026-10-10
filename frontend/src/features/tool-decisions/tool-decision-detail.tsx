/** AI 员工操作详情：处理所需的操作内容、状态、参数与结果，以及当前成员可执行的处理。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { AgentToolCallStatus, ToolIntervention, type AgentToolDecision } from "@/api"
import {
  isToolDecisionPastDeadline,
  ToolDecisionArguments,
  ToolDecisionPermission,
  useToolDecisionStatusText,
} from "@/components/agent-tool-decision"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { useDateTime } from "@/hooks/use-date-time"
import type { useToolDecisionActions } from "@/hooks/use-tool-decision-actions"
import { cn } from "@/lib/utils"

/** 展示操作的状态、级别、提交信息、完整参数与执行结果或错误。 */
export function ToolDecisionDetailContent({ decision }: { decision: AgentToolDecision }) {
  const { t } = useTranslation(["agents", "integrations", "common"])
  const { formatDateTime } = useDateTime()
  const statusText = useToolDecisionStatusText()
  // 执行失败与结果待核对以警示色显示。
  const alerting =
    decision.status === AgentToolCallStatus.AgentToolCallFailed ||
    decision.status === AgentToolCallStatus.AgentToolCallNeedsReview
  const fields: [string, ReactNode][] = [
    [
      t("toolDecisions.detail.status"),
      <span key="status" className={cn(alerting && "text-destructive")}>{statusText(decision)}</span>,
    ],
    ...(decision.level
      ? [[
          t("toolDecisions.detail.level"),
          <StatusBadge key="level" variant="muted">{t(`integrations:businessSystem.levels.${decision.level}`)}</StatusBadge>,
        ] as [string, ReactNode]]
      : []),
    [t("toolDecisions.detail.agent"), decision.agentName],
    [t("toolDecisions.columns.submittedAt"), formatDateTime(decision.createdAt)],
  ]

  return (
    <div className="space-y-6">
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-6 gap-y-3 text-sm">
        {fields.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="min-w-0 break-words">{value}</dd>
          </div>
        ))}
      </dl>
      <section className="space-y-2">
        <h3 className="text-sm font-medium">{t("toolDecisions.detail.arguments")}</h3>
        {decision.permission ? (
          <ToolDecisionPermission step={decision.permission.step} className="max-h-none" />
        ) : (
          <ToolDecisionArguments
            value={decision.arguments}
            titles={decision.argumentTitles}
            className="max-h-none"
            empty={<p className="text-sm text-muted-foreground">{t("toolDecisions.detail.noArguments")}</p>}
          />
        )}
      </section>
      {decision.error ? (
        <section className="space-y-2">
          <h3 className="text-sm font-medium">{t("toolDecisions.detail.error")}</h3>
          <p className="text-sm break-words whitespace-pre-wrap text-destructive">{decision.error}</p>
        </section>
      ) : decision.result ? (
        <section className="space-y-2">
          <h3 className="text-sm font-medium">{t("toolDecisions.detail.result")}</h3>
          <ToolDecisionArguments value={decision.result} className="max-h-none" />
        </section>
      ) : null}
    </div>
  )
}

/** 当前成员对操作可执行的处理：可裁决时拒绝与确认或批准，待核对时标记已核对，能阅读所在会话时提供打开会话。 */
export function ToolDecisionDetailActions({
  decision,
  actions,
  onOpenConversation,
  className,
}: {
  decision: AgentToolDecision
  actions: ReturnType<typeof useToolDecisionActions>
  onOpenConversation: () => void
  className?: string
}) {
  const { t } = useTranslation("agents")
  const pending = actions.isPending(decision.id)
  const decidable = decision.canDecide && !isToolDecisionPastDeadline(decision)
  if (!decidable && !decision.canReview && !decision.conversationReadable) return null
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>
      {decision.conversationReadable ? (
        <Button variant="outline" className="mr-auto" onClick={onOpenConversation}>
          {t("toolDecisions.actions.openConversation")}
        </Button>
      ) : null}
      {decidable ? (
        <>
          <Button variant="outline" disabled={pending} onClick={() => actions.rejection.select(decision)}>
            {t("toolDecisions.actions.reject")}
          </Button>
          <Button disabled={pending} onClick={() => actions.approve(decision)}>
            {t(decision.intervention === ToolIntervention.Approval
              ? "toolDecisions.actions.approve"
              : "toolDecisions.actions.confirm")}
          </Button>
        </>
      ) : decision.canReview ? (
        <Button disabled={pending} onClick={() => actions.review(decision)}>
          {t("toolDecisions.actions.review")}
        </Button>
      ) : null}
    </div>
  )
}
