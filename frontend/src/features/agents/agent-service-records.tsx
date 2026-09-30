/** AI 员工详情的服务记录：列出该 AI 员工接待的服务周期，点击行在收件箱打开对应会话。 */
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import {
  listAgentServiceSessions,
  ServiceSessionStatus,
  ServiceSource,
  type AgentServiceSession,
} from "@/api"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { useContactName } from "@/hooks/use-contact-name"
import { useDateTime } from "@/hooks/use-date-time"
import { resourceKeys } from "@/hooks/resource-keys"
import { usePagedResource } from "@/hooks/use-resource"

const pageSize = 50

/** 按开启时间倒序列出 AI 员工接待的服务周期。 */
export function AgentServiceRecords({ agentId }: { agentId: string }) {
  const { t } = useTranslation(["agents", "inbox"])
  const contactName = useContactName()
  const navigate = useNavigate()
  const { formatDateTime } = useDateTime()
  const list = usePagedResource(
    resourceKeys.agentServiceSessions({ agentId, pageSize }),
    (page) => listAgentServiceSessions(agentId, { page, pageSize }),
    {
      select: (data) => ({ items: data.sessions, page: data.page }),
      itemKey: (session) => session.serviceSessionId,
    },
  )

  /** 返回服务周期的处理结果：进行中，或结束后的是否解决。 */
  function statusText(session: AgentServiceSession) {
    if (session.status === ServiceSessionStatus.ServiceSessionStatusOpen) return t("records.open")
    if (session.resolved === null) return t("records.closed")
    return t(session.resolved ? "inbox:summaryResolved" : "inbox:summaryUnresolved")
  }

  return (
    <ResourceListLayout
      resources={list}
      errorMessage={t("records.loadError")}
      more={list.more}
    >
      <ResourceTable
        columns={[
          {
            key: "requester",
            header: t("records.requester"),
            cellClassName: "w-full max-w-0",
            cell: (session) => (
              <ResourceRowIdentity
                avatar={{ imageURL: session.requesterAvatarUrl, name: session.requesterName, fallback: "person", seed: session.requesterContactNumber }}
                name={contactName(session.requesterName, session.requesterContactNumber) || t("inbox:unknownSender")}
                secondary={
                  session.source === ServiceSource.ServiceSourceChannel
                    ? session.channelName
                    : t("inbox:filterSourceDirect")
                }
                description={session.summary || session.preview || t("performance.noQuestion")}
              />
            ),
          },
          {
            key: "status",
            header: t("records.status"),
            cellClassName: "w-px whitespace-nowrap text-muted-foreground",
            cell: statusText,
          },
          {
            key: "time",
            header: t("records.time"),
            cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
            cell: (session) =>
              session.closedAt
                ? t("records.closedAt", { time: formatDateTime(session.closedAt) })
                : t("records.openedAt", { time: formatDateTime(session.openedAt) }),
          },
        ]}
        rows={list.data?.items ?? []}
        rowKey={(session) => session.serviceSessionId}
        empty={t("records.empty")}
        onRowActivate={(session) =>
          navigate(
            `/inbox?${new URLSearchParams({ conversation: session.conversationId, message: session.openingMessageId }).toString()}`,
          )
        }
      />
    </ResourceListLayout>
  )
}
