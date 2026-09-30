/** 成员详情中该成员名下的助理及启停操作。 */
import { useTranslation } from "react-i18next"

import {
  deactivateAssistant,
  listMemberAssistants,
  reactivateAssistant,
  type AssistantData,
} from "@/api"
import { AssistantPresenceMark } from "@/components/assistant-presence-mark"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { useAccountStatusToggle } from "@/components/account-status-toggle"
import { assistantResourceKeys } from "@/hooks/use-assistant-invalidator"
import { assistantPresenceLabel } from "@/lib/assistant-presence"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 列出成员名下的助理，可逐个禁用或恢复正常；成员没有助理时不渲染。 */
export function MemberAssistantsSection({ userId }: { userId: string }) {
  const { t } = useTranslation("contacts")
  const { data } = useResource(resourceKeys.memberAssistants(userId), () => listMemberAssistants(userId))
  const statusToggle = useAccountStatusToggle<AssistantData>({
    keyPrefix: "contacts:assistants.status",
    deactivate: deactivateAssistant,
    reactivate: reactivateAssistant,
    invalidateKeys: (assistant) => assistantResourceKeys(assistant.id),
    logLabel: "修改成员的助理状态",
  })
  const assistants = data?.assistants ?? []
  if (assistants.length === 0) return null

  return (
    <section>
      <h3 className="mb-2 text-sm font-medium">{t("scopes.assistants")}</h3>
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
        ]}
        rows={assistants}
        rowKey={(assistant) => assistant.id}
        empty={t("assistants.empty")}
        rowActions={(assistant) => [statusToggle.rowAction(assistant)]}
      />
      <ConfirmationDialog {...statusToggle.dialog} />
    </section>
  )
}
