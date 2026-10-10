/** 问题会话列表：按关闭时间倒序列出问题会话，点击行在侧栏中查看对话，供 AI 表现与团队表现共用。 */
import type { QueryKey } from "@tanstack/react-query"
import { useRef } from "react"
import { useTranslation } from "react-i18next"

import type { ServiceIssueList, ServiceIssueType } from "@/api"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { useContactName } from "@/hooks/use-contact-name"
import { useDateTime } from "@/hooks/use-date-time"
import { usePagedResource } from "@/hooks/use-resource"

import { issueTypesOf } from "./report-format"
import { ServiceIssueSheet } from "./service-issue-sheet"

/** 分页读取 queryKey 对应的问题会话，行与侧栏只标出 issueTypes 中成立的问题类型。 */
export function ServiceIssueTable({
  queryKey,
  load,
  issueTypes,
  serviceSessionId,
  onIssueOpen,
}: {
  queryKey: QueryKey
  load: (page: number) => Promise<ServiceIssueList>
  issueTypes: readonly ServiceIssueType[]
  serviceSessionId: string
  onIssueOpen: (serviceSessionId: string) => void
}) {
  const { t } = useTranslation(["agents", "inbox"])
  const contactName = useContactName()
  const { formatDateTime } = useDateTime()
  // 侧栏关闭后把焦点还给打开它的行。
  const trigger = useRef<HTMLElement | null>(null)
  const list = usePagedResource(queryKey, (page) => load(page), {
    select: (data) => ({ items: data.issues, page: data.page }),
    itemKey: (row) => row.serviceSessionId,
    keepPreviousData: true,
  })

  return (
    <>
      <ResourceListLayout
        resources={list}
        errorMessage={t("performance.loadError")}
        more={list.more}
      >
        <ResourceTable
          columns={[
            {
              key: "requester",
              header: t("records.requester"),
              cellClassName: "w-full max-w-0",
              cell: (row) => (
                <ResourceRowIdentity
                  avatar={{ imageURL: row.requesterAvatarUrl, name: row.requesterName, fallback: "person", seed: row.requesterContactNumber }}
                  name={contactName(row.requesterName, row.requesterContactNumber) || t("inbox:unknownSender")}
                  secondary={row.channelName ?? t("inbox:filterSourceDirect")}
                  description={row.summary || row.preview || t("performance.noQuestion")}
                />
              ),
            },
            {
              key: "issues",
              header: t("performance.issueType"),
              cellClassName: "w-px whitespace-nowrap text-muted-foreground",
              cell: (row) =>
                issueTypesOf(row, issueTypes)
                  .map((value) => t(`performance.issueTypes.${value}`))
                  .join(" · "),
            },
            {
              key: "time",
              header: t("records.time"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (row) => t("records.closedAt", { time: formatDateTime(row.closedAt) }),
            },
          ]}
          rows={list.data?.items ?? []}
          rowKey={(row) => row.serviceSessionId}
          empty={t("performance.noIssues")}
          onRowActivate={(row) => {
            trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
            onIssueOpen(row.serviceSessionId)
          }}
        />
      </ResourceListLayout>
      <ServiceIssueSheet
        serviceSessionId={serviceSessionId}
        issueTypes={issueTypes}
        onClose={() => onIssueOpen("")}
        onCloseAutoFocus={(event) => {
          if (!trigger.current?.isConnected) return
          event.preventDefault()
          trigger.current.focus({ preventScroll: true })
        }}
      />
    </>
  )
}
