/** 部署设置的运行状态页：服务端版本、后台任务各队列的等待、执行、挂起与失败情况，以及近 7 天失败与等待重试的任务，点击任务在侧栏查看完整错误。 */
import { CircleAlertIcon } from "lucide-react"
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { getDeploymentRuntimeStatus, listDeploymentFailedTasks, type DeploymentFailedTask, type DeploymentTaskQueue } from "@/api"
import { PageHeader } from "@/components/page-header"
import { ReportSection, StatTile } from "@/components/report-parts"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useReportFormat } from "@/hooks/use-report-format"
import { usePagedResource, useResource } from "@/hooks/use-resource"

/** 运行状态的自动刷新间隔。 */
const refreshInterval = 15_000

/** 展示服务端版本与后台任务运行概况，每隔一段时间自动刷新。 */
export function DeploymentRuntimePage() {
  const { t } = useTranslation("deployment")
  const { count, duration } = useReportFormat()
  const { formatDateTime } = useDateTime()
  const [selected, setSelected] = useState<DeploymentFailedTask | null>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const status = useResource(resourceKeys.deploymentRuntime(), (signal) => getDeploymentRuntimeStatus(signal), {
    staleTime: 0,
    refetchInterval: refreshInterval,
  })
  const failed = usePagedResource(
    resourceKeys.deploymentFailedTasks({ pageSize: 50 }),
    (page, signal) => listDeploymentFailedTasks({ page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.tasks, page: data.page }), itemKey: (item) => item.id, staleTime: 0, refetchInterval: () => refreshInterval },
  )
  const queues = status.data?.queues ?? []
  const waiting = queues.reduce((sum, queue) => sum + queue.waiting, 0)
  const running = queues.reduce((sum, queue) => sum + queue.running, 0)
  const paused = queues.reduce((sum, queue) => sum + queue.paused, 0)
  const failedCount = queues.reduce((sum, queue) => sum + queue.failed, 0)
  const oldestWaitingSince = queues
    .map((queue) => queue.oldestWaitingSince)
    .filter((value): value is string => Boolean(value))
    .sort()[0]

  /** 返回最早等待任务已等待的时长说明，没有等待任务时说明无积压。 */
  function waitingDetail() {
    if (!oldestWaitingSince) return t("runtime.noBacklog")
    const seconds = Math.max(0, Math.round((Date.now() - Date.parse(oldestWaitingSince)) / 1000))
    return t("runtime.longestWait", { duration: duration(seconds) })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("runtime.title")} description={t("runtime.description")} />

      <ResourceListLayout resources={[status, failed]} errorMessage={t("runtime.loadError")} more={failed.more}>
        {status.data ? (
          <div className="mb-9 space-y-9">
            <div className="mx-3 grid grid-cols-2 gap-3 lg:grid-cols-4">
              <StatTile label={t("runtime.version")} value={status.data.version} detail={t("runtime.versionDetail")} />
              <StatTile label={t("runtime.waiting")} value={count(waiting)} detail={waitingDetail()} />
              <StatTile
                label={t("runtime.running")}
                value={count(running)}
                detail={t("runtime.pausedDetail", { formatted: count(paused) })}
              />
              <StatTile label={t("runtime.failed")} value={count(failedCount)} detail={t("runtime.failedDetail")} />
            </div>

            <ReportSection title={t("runtime.queuesTitle")} titleClassName="px-3">
              <ResourceTable<DeploymentTaskQueue>
                columns={[
                  {
                    key: "queue",
                    header: t("runtime.queueColumn"),
                    cellClassName: "min-w-0 font-mono text-sm",
                    cell: (queue) => queue.queue,
                  },
                  {
                    key: "waiting",
                    header: t("runtime.waiting"),
                    cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
                    cell: (queue) => t("runtime.waitingCell", { formatted: count(queue.waiting) }),
                  },
                  {
                    key: "running",
                    header: t("runtime.running"),
                    cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
                    cell: (queue) => t("runtime.runningCell", { formatted: count(queue.running) }),
                  },
                  {
                    key: "paused",
                    header: t("runtime.paused"),
                    cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground tabular-nums sm:table-cell",
                    cell: (queue) => t("runtime.pausedCell", { formatted: count(queue.paused) }),
                  },
                  {
                    key: "failed",
                    header: t("runtime.failed"),
                    cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
                    cell: (queue) => t("runtime.failedCell", { formatted: count(queue.failed) }),
                  },
                ]}
                rows={queues}
                rowKey={(queue) => queue.queue}
                empty={t("runtime.noQueues")}
              />
            </ReportSection>
          </div>
        ) : null}

        <ReportSection title={t("runtime.failedTasksTitle")} titleClassName="px-3">
          <ResourceTable<DeploymentFailedTask>
            columns={[
              {
                key: "task",
                header: t("runtime.taskColumn"),
                cellClassName: "min-w-0",
                cell: (task) => (
                  <ResourceRowIdentity
                    icon={CircleAlertIcon}
                    name={task.action}
                    secondary={task.workspaceName ?? t("runtime.deploymentTask")}
                    badge={task.retrying ? <StatusBadge variant="muted">{t("runtime.retrying")}</StatusBadge> : undefined}
                    description={task.error}
                  />
                ),
              },
              {
                key: "attempts",
                header: t("runtime.attemptsColumn"),
                cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground tabular-nums sm:table-cell",
                cell: (task) => t("runtime.attempts", { attempt: task.attempt, max: task.maxAttempts }),
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
            onRowActivate={(task) => {
              // 记录打开侧栏的行，关闭后把焦点还给它。
              trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
              setSelected(task)
            }}
          />
        </ReportSection>
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
                <SheetTitle className="font-mono text-base break-all">{selected.action}</SheetTitle>
                <SheetDescription>
                  {t("runtime.taskSummary", {
                    workspace: selected.workspaceName ?? t("runtime.deploymentTask"),
                    queue: selected.queue,
                    attempts: t("runtime.attempts", { attempt: selected.attempt, max: selected.maxAttempts }),
                    failedAt: t("runtime.failedAt", { time: formatDateTime(selected.failedAt) }),
                  })}
                </SheetDescription>
              </SheetHeader>
              <div className="min-h-0 flex-1 overflow-y-auto p-6">
                <h3 className="mb-2 text-sm font-medium">{t("runtime.errorTitle")}</h3>
                <pre className="font-mono text-sm break-all whitespace-pre-wrap text-muted-foreground select-text">{selected.error}</pre>
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  )
}
