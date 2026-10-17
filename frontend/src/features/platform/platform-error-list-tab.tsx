/** 概览的近期错误页签：列出等待重试与近 7 天失败的后台任务，点击在侧栏查看完整错误，页头导出诊断信息。 */
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { listPlatformFailedTasks, type PlatformFailedTask } from "@/api"
import { ResourceTable } from "@/components/resource-table"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { AdminListLayout, AdminTableFrame } from "@/features/admin/admin-list-layout"
import { AdminPageActions } from "@/features/admin/admin-page"
import { DiagnosticsExportButton } from "@/features/platform/platform-diagnostics-export"
import { runtimeRefreshInterval } from "@/features/platform/platform-runtime-tab"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useAdminPageQuery } from "@/hooks/use-admin-page-query"

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
  const failed = useAdminPageQuery(
    resourceKeys.platformFailedTasks(),
    ({ page, pageSize }, signal) => listPlatformFailedTasks({ page, pageSize }, signal),
    { staleTime: 0, refetchInterval: runtimeRefreshInterval },
  )

  /** 记录打开侧栏的行并展示错误详情，关闭后把焦点还给该行。 */
  function openDetail(detail: ErrorDetail) {
    trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setSelected(detail)
  }

  return (
    <>
      <AdminPageActions>
        <DiagnosticsExportButton />
      </AdminPageActions>
      <AdminListLayout
        resources={failed}
        errorMessage={t("runtime.loadError")}
        pagination={{
          page: failed.data?.page,
          onPageChange: failed.setPage,
          onPageSizeChange: failed.setPageSize,
          busy: failed.isPlaceholderData,
        }}
      >
        <AdminTableFrame>
        <ResourceTable<PlatformFailedTask>
          columns={[
            {
              key: "task",
              header: t("runtime.taskColumn"),
              cellClassName: "min-w-[10rem] max-w-[16rem] font-mono text-sm",
              cell: (task) => <span className="break-all">{task.action}</span>,
            },
            {
              key: "workspace",
              header: t("runtime.workspaceColumn"),
              cellClassName: "w-px whitespace-nowrap text-muted-foreground",
              cell: (task) => task.workspaceName ?? t("runtime.platformTask"),
            },
            {
              key: "queue",
              header: t("runtime.queueColumn"),
              cellClassName: "w-px whitespace-nowrap font-mono text-sm text-muted-foreground",
              cell: (task) => task.queue,
            },
            {
              key: "error",
              header: t("runtime.errorColumn"),
              cellClassName: "min-w-[12rem] max-w-[24rem]",
              cell: (task) => (
                <span className="line-clamp-2 text-muted-foreground" title={task.error}>
                  {task.error}
                </span>
              ),
            },
            {
              key: "attempts",
              header: t("runtime.attemptsColumn"),
              cellClassName: "w-px whitespace-nowrap text-right tabular-nums text-muted-foreground",
              cell: (task) => task.attempt,
            },
            {
              key: "failedAt",
              header: t("runtime.failedAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-muted-foreground",
              cell: (task) => formatDateTime(task.failedAt),
            },
          ]}
          rows={failed.data?.tasks ?? []}
          showHeader
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
        </AdminTableFrame>
      </AdminListLayout>

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
