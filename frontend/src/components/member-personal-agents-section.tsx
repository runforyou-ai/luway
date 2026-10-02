/** 成员详情中该成员负责的个人 AI 员工及启停操作。 */
import { useTranslation } from "react-i18next"

import {
  deactivatePersonalAgent,
  listMemberPersonalAgents,
  reactivatePersonalAgent,
  type PersonalAgentData,
} from "@/api"
import { PersonalAgentPresenceMark } from "@/components/personal-agent-presence-mark"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { useAccountStatusToggle } from "@/components/account-status-toggle"
import { personalAgentResourceKeys } from "@/hooks/use-personal-agent-invalidator"
import { personalAgentPresenceLabel } from "@/lib/personal-agent-presence"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 列出成员负责的个人 AI 员工，可逐个禁用或恢复正常；成员没有个人 AI 员工时不渲染。 */
export function MemberPersonalAgentsSection({ userId }: { userId: string }) {
  const { t } = useTranslation("agents")
  const { data } = useResource(resourceKeys.memberPersonalAgents(userId), () => listMemberPersonalAgents(userId))
  const statusToggle = useAccountStatusToggle<PersonalAgentData>({
    keyPrefix: "agents:personal.status",
    deactivate: deactivatePersonalAgent,
    reactivate: reactivatePersonalAgent,
    invalidateKeys: (agent) => personalAgentResourceKeys(agent.id),
    logLabel: "修改成员的个人 AI 员工状态",
  })
  const agents = data?.personalAgents ?? []
  if (agents.length === 0) return null

  return (
    <section>
      <h3 className="mb-2 text-sm font-medium">{t("personal.sectionTitle")}</h3>
      <ResourceTable
        columns={[
          {
            key: "name",
            header: t("columns.name"),
            cell: (agent) => (
              <ResourceRowIdentity
                avatar={{ imageURL: agent.avatarUrl, name: agent.displayName, fallback: "agent" }}
                mark={<PersonalAgentPresenceMark presence={agent.presence} />}
                name={agent.displayName}
                secondary={agent.device.name}
                description={personalAgentPresenceLabel(agent.presence, t)}
              />
            ),
          },
        ]}
        rows={agents}
        rowKey={(agent) => agent.id}
        empty={t("personal.empty")}
        rowActions={(agent) => [statusToggle.rowAction(agent)]}
      />
      <ConfirmationDialog {...statusToggle.dialog} />
    </section>
  )
}
