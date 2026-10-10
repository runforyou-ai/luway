/** AI 员工列表页：服务型 AI 员工与本人的个人 AI 员工共用一个列表，提供筛选、配置入口和状态管理。 */
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"

import {
  PersonalAgentPresence,
  UserStatus,
  deactivateAgent,
  deactivatePersonalAgent,
  listAgents,
  movePersonalAgent,
  reactivateAgent,
  reactivatePersonalAgent,
  type AgentListItemData,
} from "@/api"
import { AccountStatusFilter, useAccountStatusToggle } from "@/components/account-status-toggle"
import { aiModelLabel } from "@/components/ai-model-options"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import {
  ListToolbar,
  ListToolbarReset,
  ListToolbarSearch,
  ListToolbarTotal,
} from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { PersonalAgentPresenceMark } from "@/components/personal-agent-presence-mark"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable, type ResourceRowAction } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { WorkStatusDot } from "@/components/work-status"
import { usePersonalAgentPause } from "@/features/agents/personal/use-personal-agent-pause"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { contactResourceKeys } from "@/hooks/use-contact-invalidator"
import { useContactSearch } from "@/hooks/use-contact-search"
import { useDateTime } from "@/hooks/use-date-time"
import { personalAgentResourceKeys } from "@/hooks/use-personal-agent-invalidator"
import { usePagedResource, useResource } from "@/hooks/use-resource"
import { useReturnLink } from "@/hooks/use-return-to"
import { parseEnum } from "@/lib/enum"
import { personalAgentPresenceLabel } from "@/lib/personal-agent-presence"
import { chatPath } from "@/lib/workspace-paths"
import { currentComputer } from "@/platform/local-computer"

/** 显示 AI 员工列表并提供配置和状态操作；个人 AI 员工额外提供暂停和换到这台电脑。 */
export function AgentListPage() {
  const { t } = useTranslation(["agents", "common"])
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const { searchParams, setParameters, query, search, setSearch } =
    useContactSearch()
  const status =
    parseEnum(UserStatus, searchParams.get("status")) ??
    UserStatus.Active
  const statusToggle = useAccountStatusToggle<AgentListItemData>({
    keyPrefix: "agents:status",
    deactivate: deactivateAgent,
    reactivate: reactivateAgent,
    invalidateKeys: (agent) => contactResourceKeys("agent", agent.id),
    logLabel: "修改 AI 员工状态",
  })
  const personalStatusToggle = useAccountStatusToggle<AgentListItemData>({
    keyPrefix: "agents:personal.status",
    deactivate: deactivatePersonalAgent,
    reactivate: reactivatePersonalAgent,
    invalidateKeys: (agent) => personalAgentResourceKeys(agent.id),
    logLabel: "修改个人 AI 员工状态",
  })
  const { data: local } = useResource(resourceKeys.currentComputer(), () => currentComputer())
  const localComputerID = local?.computerId ?? ""
  const move = useConfirmedAction<AgentListItemData>({
    action: (agent) => movePersonalAgent(agent.id, localComputerID),
    invalidateKeys: (agent) => personalAgentResourceKeys(agent.id),
    successMessage: () => t("personal.move.done"),
    errorMessage: () => t("personal.move.error"),
    logLabel: "把个人 AI 员工换到这台电脑",
  })
  const pause = usePersonalAgentPause()

  const list = usePagedResource(
    resourceKeys.agents({ query, status, pageSize: 50 }),
    (page) => listAgents({ query, status, page, pageSize: 50 }),
    { select: (data) => ({ items: data.agents, page: data.page }), itemKey: (agent) => agent.id },
  )
  const agents = list.data?.items ?? []
  const returnLink = useReturnLink()

  /** 返回个人 AI 员工特有的行操作：桌面端换到这台电脑、暂停或恢复。 */
  function personalActions(agent: AgentListItemData): ResourceRowAction[] {
    const personal = agent.personal
    if (!personal) return []
    const active = agent.status === UserStatus.Active
    const unbound = personal.presence === PersonalAgentPresence.Unbound
    const paused = personal.presence === PersonalAgentPresence.Paused
    return [
      // 换到这台电脑只在桌面端出现。
      ...(localComputerID
        ? [{
            key: "move",
            label: t("personal.actions.move"),
            disabled: personal.computerId === localComputerID && !unbound,
            onSelect: () => move.select(agent),
          }]
        : []),
      {
        key: "pause",
        label: t(paused ? "personal.actions.resume" : "personal.actions.pause"),
        // 已禁用或未绑定电脑的个人 AI 员工不接收请求，暂停没有意义。
        disabled: !active || unbound || pause.saving,
        onSelect: () => void pause.toggle({ id: agent.id, presence: personal.presence }),
      },
    ]
  }

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
        {status !== UserStatus.Active ? (
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
                  mark={
                    agent.personal ? (
                      <PersonalAgentPresenceMark presence={agent.personal.presence} />
                    ) : (
                      <WorkStatusDot status={agent.workStatus} />
                    )
                  }
                  name={agent.displayName}
                  secondary={agent.personal ? t("form.audiences.personal") : undefined}
                  description={
                    agent.personal
                      ? [personalAgentPresenceLabel(agent.personal.presence, t), agent.personal.computerName]
                          .filter(Boolean)
                          .join(" · ")
                      : undefined
                  }
                />
              ),
            },
            {
              key: "model",
              header: t("columns.model"),
              cellClassName: "max-w-xs text-muted-foreground",
              cell: (agent) => (
                <span className="block truncate">
                  {aiModelLabel(agent.execution.managed.model)}
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
            navigate(returnLink(agent.personal ? `/ai-employees/personal/${agent.id}` : `/ai-employees/${agent.id}`))
          }
          // 已停用的 AI 员工保留禁用的发消息。
          rowActions={(agent) => [
            {
              key: "message",
              label: t("sendMessage"),
              disabled: agent.status !== UserStatus.Active,
              onSelect: () =>
                navigate(chatPath({ target: agent.identityId })),
            },
            ...personalActions(agent),
            (agent.personal ? personalStatusToggle : statusToggle).rowAction(agent),
          ]}
        />
      </ResourceListLayout>

      <ConfirmationDialog
        {...move.dialog}
        title={t("personal.move.title", { name: move.item?.displayName ?? "" })}
        description={t("personal.move.description")}
        pendingLabel={t("personal.move.saving")}
        destructive={false}
      />
      <ConfirmationDialog {...statusToggle.dialog} />
      <ConfirmationDialog {...personalStatusToggle.dialog} />
    </section>
  )
}
