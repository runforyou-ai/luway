/** 我的助理列表、在线状态与管理操作面板。 */
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"

import {
  AgentExecutionMode,
  AssistantPresence,
  UserStatus,
  currentDevice,
  deactivateAssistant,
  listAssistants,
  moveAssistant,
  reactivateAssistant,
  type AssistantData,
} from "@/api"
import { AssistantPresenceMark } from "@/components/assistant-presence-mark"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ListToolbarReset, ListToolbarSearch } from "@/components/list-toolbar"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import {
  AccountStatusFilter,
  useAccountStatusToggle,
} from "@/components/account-status-toggle"
import { assistantResourceKeys } from "@/hooks/use-assistant-invalidator"
import { assistantPresenceLabel } from "@/lib/assistant-presence"
import { localAgentName } from "@/features/contacts/assistants/local-agent-name"
import { useAssistantPause } from "@/features/contacts/assistants/use-assistant-pause"
import { ContactListSection } from "@/features/contacts/contact-list-section"
import { useContactSearch } from "@/hooks/use-contact-search"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource } from "@/hooks/use-resource"
import { useReturnLink } from "@/hooks/use-return-to"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 显示当前成员名下的助理并提供编辑、换电脑、暂停和启停操作。 */
export function AssistantsPanel() {
  const { t } = useTranslation(["contacts", "common"])
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const returnLink = useReturnLink()
  const { searchParams, setParameters, search, setSearch } = useContactSearch()
  const status =
    optionalWailsEnum(UserStatus, searchParams.get("status")) ??
    UserStatus.UserStatusActive
  const list = useResource(resourceKeys.assistants(), () => listAssistants())
  const { data: local } = useResource(resourceKeys.currentDevice(), () => currentDevice())
  const localDeviceID = local?.deviceId ?? ""
  const query = search.trim().toLowerCase()
  const assistants = (list.data?.assistants ?? []).filter(
    (assistant) =>
      assistant.status === status &&
      assistant.displayName.toLowerCase().includes(query),
  )
  const statusToggle = useAccountStatusToggle<AssistantData>({
    keyPrefix: "contacts:assistants.status",
    deactivate: deactivateAssistant,
    reactivate: reactivateAssistant,
    invalidateKeys: (assistant) => assistantResourceKeys(assistant.id),
    logLabel: "修改助理状态",
  })
  const move = useConfirmedAction<AssistantData>({
    action: (assistant) => moveAssistant(assistant.id, localDeviceID),
    invalidateKeys: (assistant) => assistantResourceKeys(assistant.id),
    successMessage: () => t("assistants.move.done"),
    errorMessage: () => t("assistants.move.error"),
    logLabel: "把助理换到这台电脑",
  })
  const pause = useAssistantPause()

  const createLabel = localDeviceID ? t("add.assistant") : t("assistants.createOnDesktop")

  return (
    <>
      <ContactListSection
        title={t("scopes.assistants")}
        description={t("scopeDescriptions.assistants")}
        scope="assistants"
        headerActions={
          localDeviceID ? (
            <Button variant="subtle" size="icon-sm" asChild>
              <Link to={returnLink("/contacts/assistants/new")} aria-label={createLabel} title={createLabel}>
                <PlusIcon />
              </Link>
            </Button>
          ) : (
            <Button variant="subtle" size="icon-sm" disabled aria-label={createLabel} title={createLabel}>
              <PlusIcon />
            </Button>
          )
        }
        toolbar={
          <>
            <ListToolbarSearch
              value={search}
              aria-label={t("search.assistants")}
              onChange={(event) => setSearch(event.target.value)}
            />
            <AccountStatusFilter value={status} setParameters={setParameters} />
            {status !== UserStatus.UserStatusActive ? (
              <ListToolbarReset onClick={() => setParameters({ status: null })}>
                {t("common:actions.clearFilters")}
              </ListToolbarReset>
            ) : null}
          </>
        }
        list={list}
      >
        <ResourceTable
          columns={[
            {
              key: "name",
              header: t("columns.name"),
              cell: (assistant) => (
                <ResourceRowIdentity
                  avatar={{ imageURL: assistant.avatarUrl, name: assistant.displayName, fallback: "agent" }}
                  mark={<AssistantPresenceMark presence={assistant.presence} />}
                  name={assistant.displayName}
                  secondary={assistant.device.name}
                  description={assistantPresenceLabel(assistant.presence, t)}
                />
              ),
            },
            {
              key: "model",
              header: t("assistants.columns.model"),
              cellClassName: "max-w-xs text-muted-foreground",
              cell: (assistant) => (
                <span className="block truncate">
                  {assistant.execution.mode === AgentExecutionMode.AgentExecutionModeLocalAgent
                    ? t("assistants.form.executorLocalAgent", { name: localAgentName(assistant.execution.localAgent.kind) })
                    : `${assistant.execution.managed.providerName} · ${assistant.execution.managed.modelName}`}
                </span>
              ),
            },
            {
              key: "time",
              header: t("common:time.addedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (assistant) =>
                t("common:time.addedAt", { time: formatDateTime(assistant.createdAt) }),
            },
          ]}
          rows={assistants}
          rowKey={(assistant) => assistant.id}
          empty={t(
            query || status !== UserStatus.UserStatusActive
              ? "assistants.emptyFiltered"
              : "assistants.empty",
          )}
          onRowActivate={(assistant) => navigate(returnLink(`/contacts/assistants/${assistant.id}`))}
          rowActions={(assistant) => {
            const active = assistant.status === UserStatus.UserStatusActive
            const paused = assistant.presence === AssistantPresence.AssistantPresencePaused
            return [
              {
                key: "message",
                label: t("sendMessage"),
                disabled: !active,
                onSelect: () => navigate(`/chats?target=${assistant.identityId}`),
              },
              {
                key: "edit",
                label: t("common:actions.edit"),
                onSelect: () => navigate(returnLink(`/contacts/assistants/${assistant.id}`)),
              },
              // 换到这台电脑只在桌面端出现。
              ...(localDeviceID
                ? [{
                    key: "move",
                    label: t("assistants.actions.move"),
                    disabled: assistant.device.id === localDeviceID && assistant.presence !== AssistantPresence.AssistantPresenceUnbound,
                    onSelect: () => move.select(assistant),
                  }]
                : []),
              {
                key: "pause",
                label: t(paused ? "assistants.actions.resume" : "assistants.actions.pause"),
                // 已禁用或未绑定电脑的助理不接收请求，暂停没有意义。
                disabled: !active || assistant.presence === AssistantPresence.AssistantPresenceUnbound || pause.saving,
                onSelect: () => void pause.toggle(assistant),
              },
              statusToggle.rowAction(assistant),
            ]
          }}
        />
      </ContactListSection>

      <ConfirmationDialog
        {...move.dialog}
        title={t("assistants.move.title", { name: move.item?.displayName ?? "" })}
        description={t("assistants.move.description")}
        pendingLabel={t("assistants.move.saving")}
        destructive={false}
      />
      <ConfirmationDialog {...statusToggle.dialog} />
    </>
  )
}
