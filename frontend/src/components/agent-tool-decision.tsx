/** AI 员工操作的展示：操作名称、参数或本机 Agent 请求权限的步骤与处理状态文案，供待处理清单与会话时间线共用。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { AgentToolCallStatus, ToolIntervention, type AgentToolCallStep, type AgentToolDecision } from "@/api"
import { useToolStatusLabel } from "@/components/agent-run-blocks"
import { useDateTime } from "@/hooks/use-date-time"
import { cn } from "@/lib/utils"

/** 状态文案需要的操作字段。 */
type ToolDecisionState = Pick<
  AgentToolDecision,
  "status" | "intervention" | "assigneeName" | "decidedByName" | "expiresAt" | "canDecide" | "localAgent"
>

/** 判断等待处理的操作已超过截止时间。 */
export function isToolDecisionPastDeadline(decision: Pick<AgentToolDecision, "status" | "expiresAt">) {
  return (
    decision.status === AgentToolCallStatus.AgentToolCallAwaitingDecision &&
    decision.expiresAt !== null &&
    new Date(decision.expiresAt).getTime() <= Date.now()
  )
}

/** 返回操作名称与所属本机 Agent、业务系统或电脑组成的标题。 */
export function toolDecisionTitle(decision: Pick<AgentToolDecision, "name" | "businessSystemName" | "computerName" | "localAgent">) {
  const source = decision.localAgent ?? decision.businessSystemName ?? decision.computerName
  return source ? `${decision.name} · ${source}` : decision.name
}

/** 返回操作当前处理状态的文案：等待中注明处理人与截止时间，已获准时按确认或审批说明执行进度，拒绝时注明处理人。 */
export function useToolDecisionStatusText() {
  const { t } = useTranslation("agents")
  const { formatDateTime } = useDateTime()
  const toolStatusLabel = useToolStatusLabel()
  return (decision: ToolDecisionState) => {
    const approval = decision.intervention === ToolIntervention.Approval
    switch (decision.status) {
      case AgentToolCallStatus.AgentToolCallAwaitingDecision: {
        if (isToolDecisionPastDeadline(decision)) return t("toolDecisions.status.expired")
        const assignee = decision.canDecide
          ? t(approval ? "toolDecisions.pending.approval" : "toolDecisions.pending.confirmation")
          : t(approval ? "toolDecisions.status.awaitingApproval" : "toolDecisions.status.awaitingConfirmation", {
              name: decision.assigneeName ?? "",
            })
        return decision.expiresAt
          ? t("toolDecisions.status.withDeadline", { status: assignee, time: formatDateTime(decision.expiresAt) })
          : assignee
      }
      case AgentToolCallStatus.AgentToolCallQueued:
      case AgentToolCallStatus.AgentToolCallRunning:
        return t(approval ? "toolDecisions.status.approvedRunning" : "toolDecisions.status.confirmedRunning")
      case AgentToolCallStatus.AgentToolCallSucceeded:
        // 本机 Agent 的权限请求获准后由本机 Agent 继续执行。
        if (decision.localAgent) {
          return decision.decidedByName ? t("toolDecisions.status.allowedBy", { name: decision.decidedByName }) : t("toolDecisions.status.allowed")
        }
        return t("toolDecisions.status.succeeded")
      case AgentToolCallStatus.AgentToolCallFailed:
        return t("toolDecisions.status.failed")
      case AgentToolCallStatus.AgentToolCallRejected:
        return decision.decidedByName
          ? t("toolDecisions.status.rejectedBy", { name: decision.decidedByName })
          : t("toolDecisions.status.rejected")
      case AgentToolCallStatus.AgentToolCallExpired:
        return t("toolDecisions.status.expired")
      case AgentToolCallStatus.AgentToolCallCancelled:
        return t("toolDecisions.status.cancelled")
      case AgentToolCallStatus.AgentToolCallNeedsReview:
        return t("toolDecisions.pending.review")
      case AgentToolCallStatus.AgentToolCallReviewed:
        return t("toolDecisions.status.reviewed")
      default:
        return toolStatusLabel(decision.status) ?? ""
    }
  }
}

/** 返回操作参数的逐项展示：JSON 对象按「参数名: 值」逐行列出，参数名有标题时显示标题，其余内容原样返回。 */
function argumentEntries(value: string, titles: AgentToolDecision["argumentTitles"]): [string, string][] | null {
  try {
    const parsed: unknown = JSON.parse(value)
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return null
    return Object.entries(parsed).map(([key, item]) => [
      titles[key] ?? key,
      typeof item === "string" ? item : JSON.stringify(item),
    ])
  } catch {
    return null
  }
}

/** 展示操作参数或结果，JSON 对象逐行列出名称与值，titles 给出参数名对应的标题，无法解析时显示原文；没有内容时显示 empty。 */
export function ToolDecisionArguments({
  value,
  titles = {},
  className,
  empty = null,
}: {
  value: string
  titles?: AgentToolDecision["argumentTitles"]
  className?: string
  empty?: ReactNode
}) {
  const entries = argumentEntries(value, titles)
  if (entries?.length === 0 || value.trim() === "") return empty
  return (
    <div className={cn("max-h-40 overflow-y-auto rounded-md bg-muted px-3 py-2 font-mono text-xs leading-5", className)}>
      {entries ? (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3">
          {entries.map(([key, item]) => (
            <div key={key} className="contents">
              <dt className="text-muted-foreground">{key}</dt>
              <dd className="break-all whitespace-pre-wrap">{item}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <pre className="break-all whitespace-pre-wrap">{value}</pre>
      )}
    </div>
  )
}

/** 展示本机 Agent 请求权限的步骤：涉及的文件、输出与文件改动的新内容。 */
export function ToolDecisionPermission({ step, className }: { step: AgentToolCallStep; className?: string }) {
  const { t } = useTranslation(["agents", "common"])
  const content = step.content ?? []
  return (
    <div className={cn("max-h-40 space-y-1.5 overflow-y-auto rounded-md bg-muted px-3 py-2 text-xs leading-5", className)}>
      {step.locations?.length ? <p className="break-all text-muted-foreground">{step.locations.join("、")}</p> : null}
      {content.map((item, index) => item.diff ? (
        <div key={index} className="min-w-0">
          <p className="break-all text-muted-foreground">
            {t(item.diff.oldText === null ? "common:localAgentFile.created" : "common:localAgentFile.changed", { path: item.diff.path })}
          </p>
          <pre className="font-mono break-all whitespace-pre-wrap">{item.diff.newText}</pre>
        </div>
      ) : (
        <pre key={index} className="font-mono break-all whitespace-pre-wrap">{item.text}</pre>
      ))}
      {!step.locations?.length && content.length === 0 ? <p className="text-muted-foreground">{t("toolDecisions.permission.noDetail")}</p> : null}
    </div>
  )
}
