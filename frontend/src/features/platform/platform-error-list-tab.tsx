/** 概览的近期错误页签：列出等待重试与近 7 天失败的后台任务，点击在侧栏查看完整错误，页头导出诊断信息。 */
import { CircleAlertIcon } from "lucide-react"
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { listPlatformFailedTasks, type PlatformFailedTask } from "@/api"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { DiagnosticsExportButton } from "@/features/platform/platform-diagnostics-export"
import { runtimeRefreshInterval } from "@/features/platform/platform-runtime-tab"
import { PlatformTabsActions } from "@/features/platform/platform-tabs"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { usePagedResource } from "@/hooks/use-resource"

/** 侧栏展示的后台任务错误详情。 */
type ErrorDetail = {
  title: string
  summary: string
  error: string
}

/** 列出等待重试与近 7 天失败的后台任务，每隔一段时间自动刷新。 */
export function PlatformErrorListTab() {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const [selected, setSelected] = useState<ErrorDetail | null>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const failed = usePagedResource(
    resourceKeys.platformFailedTasks({ pageSize: 50 }),
    (page, signal) => listPlatformFailedTasks({ page, pageSize: 50 }, signal),
    {
      select: (data) => ({ items: data.tasks, page: data.page }),
      itemKey: (item) => item.id,
      staleTime: 0,
      refetchInterval: () => runtimeRefreshInterval,
    },
  )

  /** 记录打开侧栏的行并展示错误详情，关闭后把焦点还给该行。 */
  function openDetail(detail: ErrorDetail) {
    trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setSelected(detail)
  }

  return (
    <>
      <PlatformTabsActions>
        <DiagnosticsExportButton />
      </PlatformTabsActions>
      <ResourceListLayout resources={failed} errorMessage={t("runtime.loadError")} more={failed.more}>
        <ResourceTable<PlatformFailedTask>
          columns={[
            {
              key: "task",
              header: t("runtime.taskColumn"),
              cellClassName: "min-w-0",
              cell: (task) => (
                <ResourceRowIdentity
                  icon={CircleAlertIcon}
                  name={task.action}
                  secondary={task.workspaceName ?? t("runtime.platformTask")}
                  description={task.error}
                />
              ),
            },
            {
              key: "attempts",
              header: t("runtime.attemptsColumn"),
              cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground tabular-nums sm:table-cell",
              cell: (task) => t("runtime.attempts", { attempt: task.attempt }),
            },
            {
              key: "failedAt",
              header: t("runtime.failedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
              cell: (task) => t("runtime.failedAt", { time: formatDateTime(task.failedAt) }),
            },
          ]}
          rows={failed.data?.items ?? []}
          rowKey={(task) => task.id}
          empty={t("runtime.noFailedTasks")}
          onRowActivate={(task) =>
            openDetail({
              title: task.action,
              summary: t("runtime.taskSummary", {
                workspace: task.workspaceName ?? t("runtime.platformTask"),
                queue: task.queue,
                attempts: t("runtime.attempts", { attempt: task.attempt }),
                failedAt: t("runtime.failedAt", { time: formatDateTime(task.failedAt) }),
              }),
              error: task.error,
            })
          }
        />
      </ResourceListLayout>

      <Sheet open={selected !== null} onOpenChange={(open) => (open ? undefined : setSelected(null))}>
        <SheetContent
          className="w-full gap-0 p-0 sm:max-w-xl"
          onCloseAutoFocus={(event) => {
            if (!trigger.current?.isConnected) return
            event.preventDefault()
            trigger.current.focus({ preventScroll: true })
          }}
        >
          {selected ? (
            <>
              <SheetHeader className="border-b px-6 py-4 pr-12">
                <SheetTitle className="font-mono text-base break-all">{selected.title}</SheetTitle>
                <SheetDescription>{selected.summary}</SheetDescription>
              </SheetHeader>
              <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-6">
                {selected.error ? (
                  <div>
                    <h3 className="mb-2 text-sm font-medium">{t("runtime.errorTitle")}</h3>
                    <pre className="font-mono text-sm break-all whitespace-pre-wrap text-muted-foreground select-text">{selected.error}</pre>
                  </div>
                ) : null}
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </>
  )
}
