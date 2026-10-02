/** AI 员工列表页：筛选、配置入口和状态管理。 */
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"

import {
  UserStatus,
  deactivateAgent,
  listAgents,
  reactivateAgent,
  type AgentListItemData,
} from "@/api"
import {
  ListToolbar,
  ListToolbarReset,
  ListToolbarSearch,
  ListToolbarTotal,
} from "@/components/list-toolbar"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { WorkStatusDot } from "@/components/work-status"
import { Button } from "@/components/ui/button"
import {
  AccountStatusFilter,
  useAccountStatusToggle,
} from "@/components/account-status-toggle"
import { contactResourceKeys } from "@/hooks/use-contact-invalidator"
import { useContactSearch } from "@/hooks/use-contact-search"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { usePagedResource } from "@/hooks/use-resource"
import { useReturnLink } from "@/hooks/use-return-to"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 显示 AI 员工列表并提供配置和状态操作。 */
export function AgentListPage() {
  const { t } = useTranslation(["agents", "common"])
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const { searchParams, setParameters, query, search, setSearch } =
    useContactSearch()
  const status =
    optionalWailsEnum(UserStatus, searchParams.get("status")) ??
    UserStatus.UserStatusActive
  const statusToggle = useAccountStatusToggle<AgentListItemData>({
    keyPrefix: "agents:status",
    deactivate: deactivateAgent,
    reactivate: reactivateAgent,
    invalidateKeys: (agent) => contactResourceKeys("agent", agent.id),
    logLabel: "修改 AI 员工状态",
  })

  const list = usePagedResource(
    resourceKeys.agents({ query, status, pageSize: 50 }),
    (page) => listAgents({ query, status, page, pageSize: 50 }),
    { select: (data) => ({ items: data.agents, page: data.page }), itemKey: (agent) => agent.id },
  )
  const agents = list.data?.items ?? []
  const returnLink = useReturnLink()

  return (
    <section className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("title")} description={t("description")}>
        <Button variant="subtle" size="icon-sm" asChild>
          <Link
            to={returnLink("/ai-employees/new")}
            aria-label={t("create")}
            title={t("create")}
          >
            <PlusIcon />
          </Link>
        </Button>
      </PageHeader>

      <ListToolbar>
        <ListToolbarSearch
          value={search}
          aria-label={t("search")}
          onChange={(event) => setSearch(event.target.value)}
        />
        <AccountStatusFilter value={status} setParameters={setParameters} />
        {status !== UserStatus.UserStatusActive ? (
          <ListToolbarReset
            onClick={() => setParameters({ status: null })}
          >
            {t("common:actions.clearFilters")}
          </ListToolbarReset>
        ) : null}
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout
        resources={list}
        errorMessage={t("loadError")}
        more={list.more}
      >
        <ResourceTable
          columns={[
            {
              key: "name",
              header: t("columns.name"),
              cell: (agent) => (
                <ResourceRowIdentity
                  avatar={{
                    imageURL: agent.avatarUrl,
                    name: agent.displayName,
                    fallback: "agent",
                  }}
                  mark={<WorkStatusDot status={agent.workStatus} />}
                  name={agent.displayName}
                />
              ),
            },
            {
              key: "model",
              header: t("columns.model"),
              cellClassName: "max-w-xs text-muted-foreground",
              cell: (agent) => (
                <span className="block truncate">
                  {agent.execution.managed.providerName} ·{" "}
                  {agent.execution.managed.modelName}
                </span>
              ),
            },
            {
              key: "time",
              header: t("common:time.addedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (agent) =>
                t("common:time.addedAt", { time: formatDateTime(agent.createdAt) }),
            },
          ]}
          rows={agents}
          rowKey={(agent) => agent.id}
          empty={t("empty")}
          onRowActivate={(agent) =>
            navigate(returnLink(`/ai-employees/${agent.id}`))
          }
          // 已停用的 AI 员工保留禁用的发消息。
          rowActions={(agent) => [
            {
              key: "message",
              label: t("sendMessage"),
              disabled: agent.status !== UserStatus.UserStatusActive,
              onSelect: () =>
                navigate(`/chats?target=${agent.identityId}`),
            },
            statusToggle.rowAction(agent),
          ]}
        />
      </ResourceListLayout>

      <ConfirmationDialog {...statusToggle.dialog} />
    </section>
  )
}
