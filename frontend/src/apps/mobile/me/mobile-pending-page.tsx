/** 移动端待处理：列出待本人确认、审批或核对的 AI 员工操作，逐条查看详情并处理。 */
import { useRef } from "react"
import { ChevronRightIcon, ClipboardCheckIcon, ShieldCheckIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useNavigate, useSearchParams } from "react-router"

import { AgentToolCallStatus, ToolIntervention } from "@/api"
import { mobileNotificationPath, mobilePendingPath, useMobileBack } from "@/apps/mobile/shared/mobile-navigation"
import { MobilePageHeader, MobilePageState, MobileScrollArea } from "@/apps/mobile/shared/mobile-page"
import { toolDecisionTitle } from "@/components/agent-tool-decision"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { LoadingIndicator } from "@/components/loading-indicator"
import { ToolDecisionDetailActions, ToolDecisionDetailContent } from "@/features/tool-decisions/tool-decision-detail"
import { usePendingToolDecisions } from "@/features/tool-decisions/use-pending-tool-decisions"
import { useDateTime } from "@/hooks/use-date-time"
import { useToolDecisionActions } from "@/hooks/use-tool-decision-actions"

const detailPath = `${mobilePendingPath}/detail`

/** 列出待处理的操作名称、提交的 AI 员工、需要的处理与提交时间，点击进入详情。 */
export function MobilePendingPage() {
  const { t } = useTranslation(["agents", "common"])
  const { formatDateTime } = useDateTime()
  const { data, loading, error, refresh } = usePendingToolDecisions()

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("toolDecisions.title")} backTo="/me" />
      <MobileScrollArea storageKey="pending-tool-decisions" ready={Boolean(data)}>
        {data ? (
          data.items.length ? (
            <ul className="divide-y border-b">
              {data.items.map((item) => {
                const review = item.status === AgentToolCallStatus.AgentToolCallNeedsReview
                const Icon = review ? ClipboardCheckIcon : ShieldCheckIcon
                return (
                  <li key={item.id}>
                    <Link
                      to={`${detailPath}?id=${item.id}`}
                      state={{ mobileBack: true }}
                      className="flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                    >
                      <Icon className="size-5 shrink-0 text-muted-foreground" />
                      <span className="grid min-w-0 flex-1 gap-0.5">
                        <span className="truncate text-[15px] font-medium">{toolDecisionTitle(item)}</span>
                        <span className="truncate text-xs text-muted-foreground">
                          {[
                            t("toolDecisions.submittedBy", { name: item.agentName }),
                            review
                              ? t("toolDecisions.pending.review")
                              : t(item.intervention === ToolIntervention.Approval
                                ? "toolDecisions.pending.approval"
                                : "toolDecisions.pending.confirmation"),
                          ].join(" · ")}
                        </span>
                        <span className="truncate text-xs text-muted-foreground">
                          {t("toolDecisions.submittedAt", { time: formatDateTime(item.createdAt) })}
                        </span>
                      </span>
                      <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                    </Link>
                  </li>
                )
              })}
            </ul>
          ) : (
            <MobilePageState title={t("toolDecisions.empty")} />
          )
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">{t("common:status.loading")}</LoadingIndicator>
        ) : error ? (
          <MobilePageState title={t("toolDecisions.loadError")} onRetry={() => void refresh()} />
        ) : null}
      </MobileScrollArea>
    </section>
  )
}

/** 展示地址中指定操作的完整内容并处理；操作已不在待处理清单时回到清单，处理成功后返回来源页。 */
export function MobilePendingDetailPage() {
  const { t } = useTranslation(["agents", "common"])
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const id = searchParams.get("id") ?? ""
  const back = useMobileBack(mobilePendingPath)
  const handled = useRef(false)
  const { data, loading, error, refresh } = usePendingToolDecisions()
  const actions = useToolDecisionActions({
    onDone: () => {
      handled.current = true
      back()
    },
  })
  const decision = data?.items.find((item) => item.id === id)

  // 处理成功后由返回离开，清单刷新先到达时也不另行跳转。
  if (data && !decision && !handled.current) {
    return <Navigate to={mobilePendingPath} replace />
  }

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={decision ? toolDecisionTitle(decision) : t("toolDecisions.title")} backTo={mobilePendingPath} />
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        {decision ? (
          <div className="space-y-6">
            <p className="text-sm text-muted-foreground">
              {t("toolDecisions.requested", { name: decision.agentName })}
            </p>
            <ToolDecisionDetailContent decision={decision} />
            <ToolDecisionDetailActions
              decision={decision}
              actions={actions}
              className="[&>button]:min-h-11 [&>button]:flex-1"
              onOpenConversation={() =>
                navigate(mobileNotificationPath(decision), { state: { mobileBack: true } })
              }
            />
            <ConfirmationDialog
              {...actions.rejection.dialog}
              title={t("toolDecisions.reject.title", { name: actions.rejection.item?.name ?? "" })}
              description={t("toolDecisions.reject.description")}
              pendingLabel={t("toolDecisions.reject.pending")}
            />
          </div>
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">{t("common:status.loading")}</LoadingIndicator>
        ) : error ? (
          <MobilePageState title={t("toolDecisions.loadError")} onRetry={() => void refresh()} />
        ) : null}
      </div>
    </section>
  )
}
