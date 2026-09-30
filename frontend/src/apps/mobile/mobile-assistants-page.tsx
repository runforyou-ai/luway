/** 移动端我的助理列表、详情、编辑、记忆入口与暂停、启停操作。 */
import { useState } from "react"
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useLocation, useNavigate, useParams } from "react-router"

import {
  AgentExecutionMode,
  AssistantPresence,
  deactivateAssistant,
  getAssistant,
  isNotFoundApiError,
  listAssistants,
  reactivateAssistant,
  UserStatus,
  type AssistantData,
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
import { AssistantPresenceMark } from "@/components/assistant-presence-mark"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { LoadingIndicator } from "@/components/loading-indicator"
import { ProfileAvatar } from "@/components/profile-avatar"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { useAccountStatusToggle } from "@/components/account-status-toggle"
import { AssistantEditForm } from "@/features/contacts/assistants/assistant-form"
import { localAgentName } from "@/features/contacts/assistants/local-agent-name"
import {
  assistantResourceKeys,
  useAssistantInvalidator,
} from "@/hooks/use-assistant-invalidator"
import { assistantPresenceLabel } from "@/lib/assistant-presence"
import { useAssistantPause } from "@/features/contacts/assistants/use-assistant-pause"
import { resourceKeys } from "@/hooks/resource-keys"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { useResource } from "@/hooks/use-resource"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 账号状态筛选的可选项，默认正常。 */
const statusFilters = [UserStatus.UserStatusActive, UserStatus.UserStatusInactive] as const

/** 展示当前成员名下的助理及其电脑和在线状态，按名称搜索、按账号状态筛选，点击进入详情。 */
export function MobileAssistantsPage() {
  const { t } = useTranslation(["contacts", "mobile", "common"])
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
    resourceKeys.assistants(),
    () => listAssistants(),
  )
  const query = queryText.trim().toLowerCase()
  const assistants = (data?.assistants ?? []).filter(
    (assistant) =>
      assistant.status === status &&
      assistant.displayName.toLowerCase().includes(query),
  )
  // 账号状态筛选项文案。
  const statusLabel = (value: UserStatus) =>
    t(value === UserStatus.UserStatusActive ? "statuses.active" : "statuses.inactive")

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("scopes.assistants")} backTo="/contacts" />
      <MobileSearchBar
        label={t("search.assistants")}
        value={search}
        onChange={setSearch}
      />
      <MobileFilterSheet
        summary={statusLabel(status)}
        onOpen={() => setDraftStatus(status)}
        onReset={() => setDraftStatus(UserStatus.UserStatusActive)}
        onApply={() => {
          // 切换账号状态时从目标列表顶部开始浏览。
          scrollPositions.delete(`assistants:${draftStatus}:${query}`)
          setParameters(
            { status: draftStatus === UserStatus.UserStatusActive ? null : draftStatus },
            true,
            location.state,
          )
        }}
      >
        <div
          role="group"
          aria-label={t("filters.accountStatus")}
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
        storageKey={`assistants:${status}:${query}`}
        ready={Boolean(data)}
      >
        {data ? (
          assistants.length ? (
            <ul className="divide-y border-b">
              {assistants.map((assistant) => (
                <li key={assistant.id}>
                  <Link
                    to={`/contacts/assistants/${assistant.id}`}
                    state={{ mobileBack: true }}
                    className="flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                  >
                    <span className="relative shrink-0">
                      <ProfileAvatar
                        name={assistant.displayName}
                        imageURL={assistant.avatarUrl}
                        fallback="agent"
                      />
                      <AssistantPresenceMark
                        presence={assistant.presence}
                        className="absolute -right-0.5 -bottom-0.5 ring-2 ring-background"
                      />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[15px] font-medium">
                        {assistant.displayName}
                        {assistant.device.name ? (
                          <span className="font-normal text-muted-foreground">
                            {" · "}
                            {assistant.device.name}
                          </span>
                        ) : null}
                      </span>
                      <span className="block truncate text-xs text-muted-foreground">
                        {assistantPresenceLabel(assistant.presence, t)}
                      </span>
                    </span>
                    <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                  </Link>
                </li>
              ))}
            </ul>
          ) : query || status !== UserStatus.UserStatusActive ? (
            <MobilePageState title={t("assistants.emptyFiltered")} />
          ) : (
            <MobilePageState
              title={t("assistants.empty")}
              description={t("assistants.createOnDesktop")}
            />
          )
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState
            title={t("mobile:assistants.loadError")}
            onRetry={() => void refresh()}
          />
        ) : null}
      </MobileScrollArea>
    </section>
  )
}

/** 读取助理详情，失败时区分不存在与读取错误。 */
export function MobileAssistantPage() {
  const { t } = useTranslation(["contacts", "mobile", "common"])
  const { assistantID = "" } = useParams()
  const { data, loading, error, refresh } = useResource(
    resourceKeys.assistant(assistantID),
    () => getAssistant(assistantID),
    { staleTime: 0 },
  )

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={data?.assistant.displayName ?? t("scopes.assistants")}
        backTo="/contacts/assistants"
      />
      <MobileScrollArea
        storageKey={`assistant:${assistantID}`}
        ready={Boolean(data)}
        className="px-4 py-6"
      >
        {data ? (
          <MobileAssistantDetail assistant={data.assistant} />
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState
            title={t(
              isNotFoundApiError(error)
                ? "mobile:assistants.notFound"
                : "assistants.loadError",
            )}
            onRetry={() => void refresh()}
          />
        ) : null}
      </MobileScrollArea>
    </section>
  )
}

/** 展示助理的电脑、模型和在线状态，提供编辑与记忆入口、发消息、暂停与启停。 */
function MobileAssistantDetail({ assistant }: { assistant: AssistantData }) {
  const { t } = useTranslation("contacts")
  const navigate = useNavigate()
  const pause = useAssistantPause()
  const statusToggle = useAccountStatusToggle<AssistantData>({
    keyPrefix: "contacts:assistants.status",
    deactivate: deactivateAssistant,
    reactivate: reactivateAssistant,
    invalidateKeys: (item) => assistantResourceKeys(item.id),
    logLabel: "修改助理状态",
  })
  const statusAction = statusToggle.rowAction(assistant)
  const active = assistant.status === UserStatus.UserStatusActive
  const paused = assistant.presence === AssistantPresence.AssistantPresencePaused

  return (
    <div className="space-y-9">
      <div>
        <div className="flex items-center gap-3 pb-6">
          <span className="relative shrink-0">
            <ProfileAvatar
              name={assistant.displayName}
              imageURL={assistant.avatarUrl}
              fallback="agent"
              className="size-14"
            />
            <AssistantPresenceMark
              presence={assistant.presence}
              className="absolute -right-0.5 -bottom-0.5 ring-2 ring-background"
            />
          </span>
          <div className="min-w-0 space-y-1">
            <h2 className="break-words text-lg font-semibold">
              {assistant.displayName}
            </h2>
            <p className="text-sm text-muted-foreground">
              {assistantPresenceLabel(assistant.presence, t)}
            </p>
          </div>
        </div>
        <Link
          to={`/contacts/assistants/${assistant.id}/edit`}
          state={{ mobileBack: true }}
          className="flex min-h-14 items-center gap-3 border-t text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          <span className="flex-1">{t("assistants.editTitle")}</span>
          <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
        </Link>
        <Link
          to={`/contacts/assistants/${assistant.id}/memories`}
          state={{ mobileBack: true }}
          className="flex min-h-14 items-center gap-3 border-t text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          <span className="flex-1">{t("assistants.tabs.memory")}</span>
          <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
        </Link>
        <dl className="divide-y border-y">
          <div className="py-4">
            <dt className="text-xs text-muted-foreground">
              {t("assistants.form.device")}
            </dt>
            <dd className="mt-1 break-words text-sm">
              {assistant.device.name || t("assistants.presence.unbound")}
            </dd>
          </div>
          <div className="py-4">
            <dt className="text-xs text-muted-foreground">
              {t("assistants.columns.model")}
            </dt>
            <dd className="mt-1 break-words text-sm">
              {assistant.execution.mode === AgentExecutionMode.AgentExecutionModeLocalAgent
                ? t("assistants.form.executorLocalAgent", { name: localAgentName(assistant.execution.localAgent.kind) })
                : `${assistant.execution.managed.providerName} · ${assistant.execution.managed.modelName}`}
            </dd>
          </div>
        </dl>
        <div className="flex min-h-14 items-center justify-between gap-3 border-b text-sm">
          <label
            className="flex min-h-14 flex-1 items-center"
            htmlFor="mobile-assistant-paused"
          >
            {t("assistants.actions.pause")}
          </label>
          <Switch
            id="mobile-assistant-paused"
            className="relative h-7 w-12 border-0 px-0.5 after:absolute after:inset-x-0 after:-inset-y-2 after:content-[''] [&_[data-slot=switch-thumb]]:size-6 [&_[data-slot=switch-thumb][data-state=checked]]:translate-x-5"
            checked={paused}
            // 已禁用或未绑定电脑的助理不接收请求，暂停没有意义。
            disabled={
              !active ||
              assistant.presence === AssistantPresence.AssistantPresenceUnbound ||
              pause.saving
            }
            onCheckedChange={() => void pause.toggle(assistant)}
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
                    identityId: assistant.identityId,
                    displayName: assistant.displayName,
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

/** 编辑助理的头像、名称、模型、指令和知识库，改动自动保存。 */
export function MobileAssistantEditPage() {
  const { t } = useTranslation(["contacts", "mobile", "common"])
  const { assistantID = "" } = useParams()
  const invalidate = useAssistantInvalidator()
  const { data, loading, error, refresh } = useResource(
    resourceKeys.assistant(assistantID),
    () => getAssistant(assistantID),
    { staleTime: 0 },
  )

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("assistants.editTitle")}
        backTo={`/contacts/assistants/${assistantID}`}
      />
      <div className="app-form min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        {data ? (
          <AssistantEditForm
            key={data.assistant.id}
            detail={data}
            onSaved={() => void invalidate(assistantID)}
          />
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState
            title={t(
              isNotFoundApiError(error)
                ? "mobile:assistants.notFound"
                : "assistants.loadError",
            )}
            onRetry={() => void refresh()}
          />
        ) : null}
      </div>
    </section>
  )
}
