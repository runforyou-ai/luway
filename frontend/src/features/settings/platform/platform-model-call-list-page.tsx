/** 平台设置的模型调用页：按模型、状态与工作区筛选平台模型调用记录，点开查看上游尝试与失败原因。 */
import { useState } from "react"
import { ActivityIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  AIModelCallStatus,
  listPlatformAIModelCalls,
  listPlatformAIModels,
  type AIModelCallStatusId,
  type PlatformAIModelCallData,
} from "@/api"
import { ListToolbar, ListToolbarFilter, ListToolbarSearch, ListToolbarTotal } from "@/components/list-toolbar"
import { PageHeader } from "@/components/page-header"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import {
  callPollingInterval,
  PlatformModelCallSheet,
  PlatformModelCallStatus,
} from "@/features/settings/platform/platform-model-call-sheet"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { usePagedResource, useResource } from "@/hooks/use-resource"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 可筛选的调用状态。 */
const statusOptions: AIModelCallStatusId[] = [
  AIModelCallStatus.AIModelCallStatusSucceeded,
  AIModelCallStatus.AIModelCallStatusFailed,
  AIModelCallStatus.AIModelCallStatusTimedOut,
  AIModelCallStatus.AIModelCallStatusCanceled,
  AIModelCallStatus.AIModelCallStatusRunning,
]

/** 列出平台模型调用：主行为模型与工作区，第二行为用途与 Token 用量，非成功状态在名称旁标记。 */
export function PlatformModelCallListPage() {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams()
  const status = optionalWailsEnum(AIModelCallStatus, searchParams.get("status")) ?? AIModelCallStatus.$zero
  const modelId = searchParams.get("model") ?? ""
  const [openCallId, setOpenCallId] = useState("")
  const models = useResource(resourceKeys.platformAIModels(), (signal) => listPlatformAIModels(signal))
  const list = usePagedResource(
    resourceKeys.platformAIModelCalls({ query, status, modelId, pageSize: 50 }),
    (page, signal) => listPlatformAIModelCalls({ query, status, modelId, page, pageSize: 50 }, signal),
    {
      select: (data) => ({ items: data.calls, page: data.page }),
      itemKey: (item) => item.id,
      // 进入页面时重新读取，有进行中的调用时定时刷新直到全部结束。
      staleTime: 0,
      refetchInterval: (items) =>
        items.some((item) => item.status === AIModelCallStatus.AIModelCallStatusRunning) ? callPollingInterval : false,
    },
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("platformCalls.title")} description={t("platformCalls.description")} />

      <ListToolbar>
        <ListToolbarSearch value={search} aria-label={t("platformCalls.search")} onChange={(event) => setSearch(event.target.value)} />
        <ListToolbarFilter
          label={t("platformCalls.modelFilter")}
          allLabel={t("platformCalls.all")}
          value={modelId}
          options={(models.data ?? []).map((model) => ({ value: model.id, label: model.name }))}
          onValueChange={(next) => setParameters({ model: next || null })}
        />
        <ListToolbarFilter
          label={t("platformCalls.statusFilter")}
          allLabel={t("platformCalls.all")}
          value={status}
          options={statusOptions.map((value) => ({ value, label: t(`platformCalls.statuses.${value}`) }))}
          onValueChange={(next) => setParameters({ status: next || null })}
        />
        <ListToolbarTotal count={list.data?.total} />
      </ListToolbar>

      <ResourceListLayout resources={list} errorMessage={t("platformCalls.loadError")} more={list.more}>
        <ResourceTable<PlatformAIModelCallData>
          columns={[
            {
              key: "call",
              header: t("platformCalls.title"),
              cellClassName: "min-w-0",
              cell: (call) => (
                <ResourceRowIdentity
                  icon={ActivityIcon}
                  name={call.modelName}
                  secondary={call.workspaceName}
                  badge={
                    call.status === AIModelCallStatus.AIModelCallStatusSucceeded ? undefined : (
                      <PlatformModelCallStatus status={call.status} />
                    )
                  }
                  description={[
                    t(`platformCalls.usages.${call.usage}`),
                    t("platformCalls.tokens", {
                      input: call.inputTokens.toLocaleString(),
                      output: call.outputTokens.toLocaleString(),
                    }),
                    call.attemptCount > 1 ? t("platformCalls.attempts", { count: call.attemptCount }) : null,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                />
              ),
            },
            {
              key: "time",
              header: t("platformCalls.sheet.startedAt"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
              cell: (call) => formatDateTime(call.createdAt),
            },
          ]}
          rows={list.data?.items ?? []}
          rowKey={(call) => call.id}
          empty={t("platformCalls.empty")}
          onRowActivate={(call) => setOpenCallId(call.id)}
        />
      </ResourceListLayout>

      <PlatformModelCallSheet callId={openCallId} onClose={() => setOpenCallId("")} />
    </div>
  )
}
