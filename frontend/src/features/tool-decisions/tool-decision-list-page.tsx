/** 待处理页：待当前成员确认、审批或核对的 AI 员工操作，在侧栏查看详情并处理。 */
import { ClipboardCheckIcon, ShieldCheckIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate, useSearchParams } from "react-router"
import { toast } from "sonner"

import { AgentToolCallStatus, ToolIntervention, type AgentToolDecision } from "@/api"
import { toolDecisionTitle } from "@/components/agent-tool-decision"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable, type ResourceRowAction } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { ToolDecisionDetailActions, ToolDecisionDetailContent } from "@/features/tool-decisions/tool-decision-detail"
import { useDecisionLink } from "@/features/tool-decisions/use-decision-link"
import { usePendingToolDecisions } from "@/features/tool-decisions/use-pending-tool-decisions"
import { useDateTime } from "@/hooks/use-date-time"
import { useToolDecisionActions } from "@/hooks/use-tool-decision-actions"
import { notificationPath } from "@/lib/workspace-paths"

/** 列出待当前成员处理的 AI 员工操作，点击在侧栏查看完整内容并处理，能阅读所在会话时可打开会话。 */
export function ToolDecisionListPage() {
  const { t } = useTranslation(["agents", "integrations"])
  const navigate = useNavigate()
  const { formatDateTime } = useDateTime()
  const resource = usePendingToolDecisions()
  const items = resource.data?.items ?? []
  // 侧栏展示的操作与地址中的 decision 参数同步，外部链接可直接打开一条操作；操作已由他人处理、过期或离开清单时关闭侧栏。
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedID = searchParams.get("decision")
  const current = selectedID ? (items.find((item) => item.id === selectedID) ?? null) : null
  const actions = useToolDecisionActions({ onDone: () => setSelected(null) })

  // 链接指向的操作已处理、过期或不属于当前成员时提示并清除参数。
  useDecisionLink({
    decisionID: selectedID,
    loaded: Boolean(resource.data),
    found: current !== null,
    refreshing: resource.refreshing,
    updatedAt: resource.dataUpdatedAt,
    refresh: resource.refresh,
    onUnavailable: () => {
      toast.info(t("toolDecisions.linkUnavailable"))
      setSelected(null)
    },
  })

  /** 选中或关闭侧栏中的操作并同步地址。 */
  function setSelected(item: AgentToolDecision | null) {
    const next = new URLSearchParams(searchParams)
    if (item) next.set("decision", item.id)
    else next.delete("decision")
    setSearchParams(next, { replace: true })
  }

  /** 打开操作所在会话。 */
  function openConversation(item: AgentToolDecision) {
    navigate(notificationPath(item))
  }

  /** 返回一条操作的行操作：查看详情，能阅读所在会话时打开会话。 */
  function rowActions(item: AgentToolDecision): ResourceRowAction[] {
    return [
      { key: "detail", label: t("toolDecisions.actions.detail"), onSelect: () => setSelected(item) },
      ...(item.conversationReadable
        ? [{ key: "conversation", label: t("toolDecisions.actions.openConversation"), onSelect: () => openConversation(item) }]
        : []),
    ]
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("toolDecisions.title")} description={t("toolDecisions.description")} />
      <ResourceListLayout resources={resource} errorMessage={t("toolDecisions.loadError")}>
        <ResourceTable
          columns={[
            {
              key: "operation",
              header: t("toolDecisions.columns.operation"),
              cellClassName: "min-w-0",
              cell: (item) => {
                const review = item.status === AgentToolCallStatus.AgentToolCallNeedsReview
                return (
                  <ResourceRowIdentity
                    icon={review ? ClipboardCheckIcon : ShieldCheckIcon}
                    name={toolDecisionTitle(item)}
                    badge={item.level ? (
                      <StatusBadge variant="muted">{t(`integrations:businessSystem.levels.${item.level}`)}</StatusBadge>
                    ) : null}
                    description={[
                      t("toolDecisions.submittedBy", { name: item.agentName }),
                      review
                        ? t("toolDecisions.pending.review")
                        : t(item.intervention === ToolIntervention.Approval
                          ? "toolDecisions.pending.approval"
                          : "toolDecisions.pending.confirmation"),
                    ].join(" · ")}
                  />
                )
              },
            },
            {
              key: "expiresAt",
              header: t("toolDecisions.columns.expiresAt"),
              cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground sm:table-cell",
              headerClassName: "hidden sm:table-cell",
              cell: (item) =>
                item.status === AgentToolCallStatus.AgentToolCallAwaitingDecision && item.expiresAt
                  ? t("toolDecisions.expiresAt", { time: formatDateTime(item.expiresAt) })
                  : null,
            },
            {
              key: "submittedAt",
              header: t("toolDecisions.columns.submittedAt"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (item) => t("toolDecisions.submittedAt", { time: formatDateTime(item.createdAt) }),
            },
          ]}
          rows={items}
          rowKey={(item) => item.id}
          empty={t("toolDecisions.empty")}
          onRowActivate={setSelected}
          rowActions={rowActions}
        />
      </ResourceListLayout>

      <Sheet open={current !== null} onOpenChange={(open) => (open ? undefined : setSelected(null))}>
        <SheetContent className="w-full gap-0 p-0 sm:max-w-lg">
          <SheetHeader className="border-b px-6 py-4 pr-12">
            <SheetTitle className="break-all">{current ? toolDecisionTitle(current) : ""}</SheetTitle>
            <SheetDescription>
              {current ? t("toolDecisions.requested", { name: current.agentName }) : ""}
            </SheetDescription>
          </SheetHeader>
          <ScrollArea className="min-h-0 flex-1">
            <div className="p-6">{current ? <ToolDecisionDetailContent decision={current} /> : null}</div>
          </ScrollArea>
          {current ? (
            <ToolDecisionDetailActions
              decision={current}
              actions={actions}
              className="justify-end border-t px-6 py-4"
              onOpenConversation={() => openConversation(current)}
            />
          ) : null}
        </SheetContent>
      </Sheet>

      <ConfirmationDialog
        {...actions.rejection.dialog}
        title={t("toolDecisions.reject.title", { name: actions.rejection.item?.name ?? "" })}
        description={t("toolDecisions.reject.description")}
        pendingLabel={t("toolDecisions.reject.pending")}
      />
    </div>
  )
}
