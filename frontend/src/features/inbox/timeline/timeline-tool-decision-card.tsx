/** 时间线中 AI 员工提交的待确认或待审批操作卡片。 */
import { ShieldCheckIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { AgentToolCallStatus, ToolIntervention, type AgentToolDecision } from "@/api"
import {
  isToolDecisionPastDeadline,
  ToolDecisionArguments,
  ToolDecisionPermission,
  toolDecisionTitle,
  useToolDecisionStatusText,
} from "@/components/agent-tool-decision"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { useToolDecisionActions } from "@/hooks/use-tool-decision-actions"
import { cn } from "@/lib/utils"

/** 展示操作内容、参数与当前处理状态；当前成员可处理时在状态行末尾提供确认或批准、拒绝，待核对时提供标记已核对。 */
export function TimelineToolDecisionCard({
  decision,
  conversationID,
}: {
  decision: AgentToolDecision
  conversationID: string
}) {
  const { t } = useTranslation(["agents", "integrations"])
  const statusText = useToolDecisionStatusText()
  const actions = useToolDecisionActions({ timelineConversationId: conversationID })
  const pending = actions.isPending(decision.id)
  const approval = decision.intervention === ToolIntervention.Approval
  // 执行失败与结果待核对以警示色显示。
  const alerting =
    decision.status === AgentToolCallStatus.AgentToolCallFailed ||
    decision.status === AgentToolCallStatus.AgentToolCallNeedsReview
  const decidable = decision.canDecide && !isToolDecisionPastDeadline(decision)

  return (
    <section
      aria-label={toolDecisionTitle(decision)}
      className="w-full max-w-md rounded-lg border bg-background px-4 py-3 text-left text-sm text-foreground"
    >
      <div className="flex items-start gap-2.5">
        <ShieldCheckIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="text-xs text-muted-foreground">
            {t("toolDecisions.requested", { name: decision.agentName })}
          </p>
          <p className="mt-0.5 font-medium break-all">{toolDecisionTitle(decision)}</p>
        </div>
        {decision.level ? (
          <StatusBadge variant="muted">{t(`integrations:businessSystem.levels.${decision.level}`)}</StatusBadge>
        ) : null}
      </div>
      {decision.permission ? (
        <ToolDecisionPermission step={decision.permission.step} className="mt-2.5" />
      ) : (
        <ToolDecisionArguments value={decision.arguments} titles={decision.argumentTitles} className="mt-2.5" />
      )}
      {/* 状态行固定最小高度，处理按钮出现或消失时卡片高度不变。 */}
      <div className="mt-3 flex min-h-7 items-center gap-2">
        <p className={cn("min-w-0 flex-1 text-xs text-muted-foreground", alerting && "text-destructive")}>
          {statusText(decision)}
        </p>
        {decidable ? (
          <>
            <Button
              size="sm"
              variant="outline"
              disabled={pending}
              onClick={() => actions.rejection.select(decision)}
            >
              {t("toolDecisions.actions.reject")}
            </Button>
            <Button size="sm" disabled={pending} onClick={() => actions.approve(decision)}>
              {t(approval ? "toolDecisions.actions.approve" : "toolDecisions.actions.confirm")}
            </Button>
          </>
        ) : decision.canReview ? (
          <Button size="sm" variant="outline" disabled={pending} onClick={() => actions.review(decision)}>
            {t("toolDecisions.actions.review")}
          </Button>
        ) : null}
      </div>
      {decision.status === AgentToolCallStatus.AgentToolCallFailed && decision.error ? (
        <p className="mt-1 text-xs break-words whitespace-pre-wrap text-destructive">{decision.error}</p>
      ) : null}

      <ConfirmationDialog
        {...actions.rejection.dialog}
        title={t("toolDecisions.reject.title", { name: decision.name })}
        description={t("toolDecisions.reject.description")}
        pendingLabel={t("toolDecisions.reject.pending")}
      />
    </section>
  )
}
