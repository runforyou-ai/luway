/** 移动端我的个人 AI 员工列表、详情、编辑、记忆入口与暂停、启停操作。 */
import { useState } from "react"
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useLocation, useNavigate, useParams } from "react-router"

import {
  AgentExecutionMode,
  PersonalAgentPresence,
  deactivatePersonalAgent,
  getPersonalAgent,
  isNotFoundApiError,
  listPersonalAgents,
  reactivatePersonalAgent,
  UserStatus,
  type PersonalAgentData,
} from "@/api"
import type { MobileAgentLocationState } from "@/apps/mobile/mobile-agent-chat-page"
import { MobileFilterSheet } from "@/apps/mobile/mobile-filter-sheet"
import { useMobileNavigation } from "@/apps/mobile/mobile-navigation"
import {
  MobilePageHeader,
  MobilePageState,
  MobileScrollArea,
  MobileSearchBar,
} from "@/apps/mobile/mobile-page"
import { PersonalAgentPresenceMark } from "@/components/personal-agent-presence-mark"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { LoadingIndicator } from "@/components/loading-indicator"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { useAccountStatusToggle } from "@/components/account-status-toggle"
import { PersonalAgentEditForm } from "@/features/agents/personal/personal-agent-form"
import { localAgentName } from "@/lib/local-agent-name"
import {
  personalAgentResourceKeys,
  usePersonalAgentInvalidator,
} from "@/hooks/use-personal-agent-invalidator"
import { personalAgentPresenceLabel } from "@/lib/personal-agent-presence"
import { usePersonalAgentPause } from "@/features/agents/personal/use-personal-agent-pause"
import { resourceKeys } from "@/hooks/resource-keys"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { useResource } from "@/hooks/use-resource"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 账号状态筛选的可选项，默认正常。 */
const statusFilters = [UserStatus.UserStatusActive, UserStatus.UserStatusInactive] as const

/** 展示当前成员负责的个人 AI 员工及其电脑和在线状态，按名称搜索、按账号状态筛选，点击进入详情。 */
export function MobilePersonalAgentsPage() {
  const { t } = useTranslation(["agents", "contacts", "mobile", "common"])
  const location = useLocation()
  const { scrollPositions } = useMobileNavigation()
  const {
    searchParams,
    setParameters,
    query: queryText,
    search,
    setSearch,
  } = useListSearchParams()
  const status =
    optionalWailsEnum(UserStatus, searchParams.get("status")) ??
    UserStatus.UserStatusActive
  const [draftStatus, setDraftStatus] = useState<UserStatus>(status)
  const { data, loading, error, refresh } = useResource(
    resourceKeys.personalAgents(),
    () => listPersonalAgents(),
  )
  const query = queryText.trim().toLowerCase()
  const agents = (data?.personalAgents ?? []).filter(
    (agent) =>
      agent.status === status &&
      agent.displayName.toLowerCase().includes(query),
  )
  // 账号状态筛选项文案。
  const statusLabel = (value: UserStatus) =>
    t(value === UserStatus.UserStatusActive ? "contacts:statuses.active" : "contacts:statuses.inactive")

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("personal.sectionTitle")} backTo="/me" />
      <MobileSearchBar
        label={t("search")}
        value={search}
        onChange={setSearch}
      />
      <MobileFilterSheet
        summary={statusLabel(status)}
        onOpen={() => setDraftStatus(status)}
        onReset={() => setDraftStatus(UserStatus.UserStatusActive)}
        onApply={() => {
          // 切换账号状态时从目标列表顶部开始浏览。
          scrollPositions.delete(`agents:${draftStatus}:${query}`)
          setParameters(
            { status: draftStatus === UserStatus.UserStatusActive ? null : draftStatus },
            true,
            location.state,
          )
        }}
      >
        <div
          role="group"
          aria-label={t("contacts:filters.accountStatus")}
          className="grid grid-cols-2 gap-2"
        >
          {statusFilters.map((value) => (
            <Button
              key={value}
              variant={draftStatus === value ? "default" : "outline"}
              className="min-h-11"
              aria-pressed={draftStatus === value}
              onClick={() => setDraftStatus(value)}
            >
              {statusLabel(value)}
            </Button>
          ))}
        </div>
      </MobileFilterSheet>
      <MobileScrollArea
        storageKey={`agents:${status}:${query}`}
        ready={Boolean(data)}
      >
        {data ? (
          agents.length ? (
            <ul className="divide-y border-b">
              {agents.map((agent) => (
                <li key={agent.id}>
                  <Link
                    to={`/me/personal-agents/${agent.id}`}
                    state={{ mobileBack: true }}
                    className="flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                  >
                    <span className="relative shrink-0">
                      <ProfileAvatar
                        name={agent.displayName}
                        imageURL={agent.avatarUrl}
                        fallback="agent"
                      />
                      <PersonalAgentPresenceMark
                        presence={agent.presence}
                        className="absolute -right-0.5 -bottom-0.5 ring-2 ring-background"
                      />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[15px] font-medium">
                        {agent.displayName}
                        {agent.device.name ? (
                          <span className="font-normal text-muted-foreground">
                            {" · "}
                            {agent.device.name}
                          </span>
                        ) : null}
                      </span>
                      <span className="block truncate text-xs text-muted-foreground">
                        {personalAgentPresenceLabel(agent.presence, t)}
                      </span>
                    </span>
                    <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                  </Link>
                </li>
              ))}
            </ul>
          ) : query || status !== UserStatus.UserStatusActive ? (
            <MobilePageState title={t("personal.emptyFiltered")} />
          ) : (
            <MobilePageState
              title={t("personal.empty")}
              description={t("personal.createOnDesktop")}
            />
          )
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState
            title={t("mobile:personalAgents.loadError")}
            onRetry={() => void refresh()}
          />
        ) : null}
      </MobileScrollArea>
    </section>
  )
}

/** 读取个人 AI 员工详情，失败时区分不存在与读取错误。 */
export function MobilePersonalAgentPage() {
  const { t } = useTranslation(["agents", "contacts", "mobile", "common"])
  const { agentID = "" } = useParams()
  const { data, loading, error, refresh } = useResource(
    resourceKeys.personalAgent(agentID),
    () => getPersonalAgent(agentID),
    { staleTime: 0 },
  )

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={data?.personalAgent.displayName ?? t("personal.sectionTitle")}
        backTo="/me/personal-agents"
      />
      <MobileScrollArea
        storageKey={`agent:${agentID}`}
        ready={Boolean(data)}
        className="px-4 py-6"
      >
        {data ? (
          <MobilePersonalAgentDetail agent={data.personalAgent} />
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState
            title={t(
              isNotFoundApiError(error)
                ? "mobile:personalAgents.notFound"
                : "personal.loadError",
            )}
            onRetry={() => void refresh()}
          />
        ) : null}
      </MobileScrollArea>
    </section>
  )
}

/** 展示个人 AI 员工的电脑、模型和在线状态，提供编辑与记忆入口、发消息、暂停与启停。 */
function MobilePersonalAgentDetail({ agent }: { agent: PersonalAgentData }) {
  const { t } = useTranslation(["agents", "contacts"])
  const navigate = useNavigate()
  const pause = usePersonalAgentPause()
  const statusToggle = useAccountStatusToggle<PersonalAgentData>({
    keyPrefix: "agents:personal.status",
    deactivate: deactivatePersonalAgent,
    reactivate: reactivatePersonalAgent,
    invalidateKeys: (item) => personalAgentResourceKeys(item.id),
    logLabel: "修改个人 AI 员工状态",
  })
  const statusAction = statusToggle.rowAction(agent)
  const active = agent.status === UserStatus.UserStatusActive
  const paused = agent.presence === PersonalAgentPresence.PersonalAgentPresencePaused

  return (
    <div className="space-y-9">
      <div>
        <div className="flex items-center gap-3 pb-6">
          <span className="relative shrink-0">
            <ProfileAvatar
              name={agent.displayName}
              imageURL={agent.avatarUrl}
              fallback="agent"
              className="size-14"
            />
            <PersonalAgentPresenceMark
              presence={agent.presence}
              className="absolute -right-0.5 -bottom-0.5 ring-2 ring-background"
            />
          </span>
          <div className="min-w-0 space-y-1">
            <h2 className="break-words text-lg font-semibold">
              {agent.displayName}
            </h2>
            <p className="text-sm text-muted-foreground">
              {personalAgentPresenceLabel(agent.presence, t)}
            </p>
          </div>
        </div>
        <Link
          to={`/me/personal-agents/${agent.id}/edit`}
          state={{ mobileBack: true }}
          className="flex min-h-14 items-center gap-3 border-t text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          <span className="flex-1">{t("personal.editTitle")}</span>
          <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
        </Link>
        <Link
          to={`/me/personal-agents/${agent.id}/memories`}
          state={{ mobileBack: true }}
          className="flex min-h-14 items-center gap-3 border-t text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          <span className="flex-1">{t("personal.tabs.memory")}</span>
          <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
        </Link>
        <dl className="divide-y border-y">
          <div className="py-4">
            <dt className="text-xs text-muted-foreground">
              {t("personal.form.device")}
            </dt>
            <dd className="mt-1 break-words text-sm">
              {agent.device.name || t("personal.presence.unbound")}
            </dd>
          </div>
          <div className="py-4">
            <dt className="text-xs text-muted-foreground">
              {t("columns.model")}
            </dt>
            <dd className="mt-1 break-words text-sm">
              {agent.execution.mode === AgentExecutionMode.AgentExecutionModeLocalAgent
                ? t("personal.form.executorLocalAgent", { name: localAgentName(agent.execution.localAgent.kind) })
                : `${agent.execution.managed.providerName} · ${agent.execution.managed.modelName}`}
            </dd>
          </div>
        </dl>
        <div className="flex min-h-14 items-center justify-between gap-3 border-b text-sm">
          <label
            className="flex min-h-14 flex-1 items-center"
            htmlFor="mobile-agent-paused"
          >
            {t("personal.actions.pause")}
          </label>
          <Switch
            id="mobile-agent-paused"
            className="relative h-7 w-12 border-0 px-0.5 after:absolute after:inset-x-0 after:-inset-y-2 after:content-[''] [&_[data-slot=switch-thumb]]:size-6 [&_[data-slot=switch-thumb][data-state=checked]]:translate-x-5"
            checked={paused}
            // 已禁用或未绑定电脑的个人 AI 员工不接收请求，暂停没有意义。
            disabled={
              !active ||
              agent.presence === PersonalAgentPresence.PersonalAgentPresenceUnbound ||
              pause.saving
            }
            onCheckedChange={() => void pause.toggle(agent)}
          />
        </div>
      </div>
      <div className="space-y-3">
        {active ? (
          <Button
            type="button"
            className="min-h-11 w-full"
            onClick={() =>
              void navigate(`/chats/agent/${crypto.randomUUID()}`, {
                state: {
                  draftTarget: {
                    identityId: agent.identityId,
                    displayName: agent.displayName,
                  },
                  mobileBack: true,
                } satisfies MobileAgentLocationState,
              })
            }
          >
            {t("sendMessage")}
          </Button>
        ) : null}
        <Button
          type="button"
          variant={statusAction.destructive ? "destructive" : "outline"}
          className="min-h-11 w-full"
          onClick={statusAction.onSelect}
        >
          {statusAction.label}
        </Button>
      </div>
      <ConfirmationDialog {...statusToggle.dialog} />
    </div>
  )
}

/** 编辑个人 AI 员工的头像、名称、模型、指令和知识库，改动自动保存。 */
export function MobilePersonalAgentEditPage() {
  const { t } = useTranslation(["agents", "contacts", "mobile", "common"])
  const { agentID = "" } = useParams()
  const invalidate = usePersonalAgentInvalidator()
  const { data, loading, error, refresh } = useResource(
    resourceKeys.personalAgent(agentID),
    () => getPersonalAgent(agentID),
    { staleTime: 0 },
  )

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("personal.editTitle")}
        backTo={`/me/personal-agents/${agentID}`}
      />
      <div className="app-form min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        {data ? (
          <PersonalAgentEditForm
            key={data.personalAgent.id}
            detail={data}
            onSaved={() => void invalidate(agentID)}
          />
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState
            title={t(
              isNotFoundApiError(error)
                ? "mobile:personalAgents.notFound"
                : "personal.loadError",
            )}
            onRetry={() => void refresh()}
          />
        ) : null}
      </div>
    </section>
  )
}
