/** 平台设置的运行状态页：各服务端进程的版本与心跳，对象存储与授权服务的状态，后台任务各队列的等待、执行、挂起与失败情况，以及近期错误中等待重试与近 7 天失败的任务和近 7 天的服务端错误；点击任务、服务端错误或异常的依赖在侧栏查看完整错误，页头导出诊断信息。 */
import { CircleAlertIcon, CloudIcon, KeyRoundIcon, ServerIcon, type LucideIcon } from "lucide-react"
import { useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate, useSearchParams } from "react-router"
import { toast } from "sonner"

import {
  getPlatformDiagnostics,
  getPlatformRuntimeStatus,
  isApiError,
  listPlatformFailedTasks,
  listPlatformServerErrors,
  type PlatformFailedTask,
  type PlatformRuntimeStatus,
  type PlatformServer,
  type PlatformServerError,
  type PlatformTaskQueue,
} from "@/api"
import { PageHeader } from "@/components/page-header"
import { ReportSection, StatTile } from "@/components/report-parts"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useReportFormat } from "@/hooks/use-report-format"
import { usePagedResource, useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { cn } from "@/lib/utils"
import { saveTextFile } from "@/platform/save-file"

/** 运行状态的自动刷新间隔。 */
const refreshInterval = 15_000

/** 侧栏展示的错误详情；mono 表示标题是任务名等标识，用等宽字体显示；fields 是错误信息之前列出的名称与取值。 */
type ErrorDetail = {
  title: string
  mono?: boolean
  summary: string
  fields?: [string, string][]
  error: string
}

/** 近期错误的页签，与地址参数 errors 同步。 */
const errorTabs = ["tasks", "server"] as const

/** 外部依赖列表的一行。 */
type DependencyRow = {
  key: string
  icon: LucideIcon
  name: string
  badge: ReactNode
  description: string
  detail: ErrorDetail | null
}

/** 展示服务端进程、外部依赖与后台任务运行状态，每隔一段时间自动刷新。 */
export function PlatformRuntimePage() {
  const { t } = useTranslation("platform")
  const { count, duration } = useReportFormat()
  const { formatDateTime } = useDateTime()
  const navigate = useNavigate()
  const [selected, setSelected] = useState<ErrorDetail | null>(null)
  const location = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()
  const errorTab = errorTabs.find((value) => value === searchParams.get("errors")) ?? errorTabs[0]
  // 滚动位置不随近期错误的页签变化，切换页签时保持当前浏览位置。
  const scrollParams = new URLSearchParams(searchParams)
  scrollParams.delete("errors")
  const [exporting, setExporting] = useState(false)
  const trigger = useRef<HTMLElement | null>(null)
  const status = useResource(resourceKeys.platformRuntime(), (signal) => getPlatformRuntimeStatus(signal), {
    staleTime: 0,
    refetchInterval: refreshInterval,
  })
  const failed = usePagedResource(
    resourceKeys.platformFailedTasks({ pageSize: 50 }),
    (page, signal) => listPlatformFailedTasks({ page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.tasks, page: data.page }), itemKey: (item) => item.id, staleTime: 0, refetchInterval: () => refreshInterval },
  )
  const serverErrors = usePagedResource(
    resourceKeys.platformServerErrors({ pageSize: 50 }),
    (page, signal) => listPlatformServerErrors({ page, pageSize: 50 }, signal),
    { select: (data) => ({ items: data.errors, page: data.page }), itemKey: (item) => item.id, staleTime: 0, refetchInterval: () => refreshInterval },
  )
  const queues = status.data?.queues ?? []
  const waiting = queues.reduce((sum, queue) => sum + queue.waiting, 0)
  const running = queues.reduce((sum, queue) => sum + queue.running, 0)
  const retrying = queues.reduce((sum, queue) => sum + queue.retrying, 0)
  const paused = queues.reduce((sum, queue) => sum + queue.paused, 0)
  const failedCount = queues.reduce((sum, queue) => sum + queue.failed, 0)
  const oldestWaitingSince = queues
    .map((queue) => queue.oldestWaitingSince)
    .filter((value): value is string => Boolean(value))
    .sort()[0]
  const servers = status.data?.servers ?? []
  const onlineServers = servers.filter((server) => server.online)
  const versions = new Set(onlineServers.map((server) => server.version))

  /** 记录打开侧栏的行并展示错误详情，关闭后把焦点还给该行。 */
  function openDetail(detail: ErrorDetail) {
    trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setSelected(detail)
  }

  /** 生成诊断信息并保存为以导出时间命名的 JSON 文件，原生端取消保存时不提示。 */
  async function exportDiagnostics() {
    setExporting(true)
    try {
      const diagnostics = await getPlatformDiagnostics()
      // 文件名使用本地时间，格式为 diagnostics-YYYYMMDD-HHmmss.json。
      const now = new Date()
      const pad = (value: number) => String(value).padStart(2, "0")
      const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
      const saved = await saveTextFile(`diagnostics-${stamp}.json`, JSON.stringify(diagnostics, null, 2), "application/json")
      if (saved) toast.success(t("runtime.diagnosticsExported"))
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("导出诊断信息失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error, []) : t("runtime.exportDiagnosticsError"))
    } finally {
      setExporting(false)
    }
  }

  /** 返回在线服务器的版本说明，并附上失联与 NATS 连接异常的服务器数。 */
  function serversDetail() {
    const lost = servers.length - onlineServers.length
    const disconnected = onlineServers.filter((server) => !server.tasksNatsConnected || !server.realtimeNatsConnected).length
    const parts = [
      versions.size === 0
        ? t("runtime.noServers")
        : versions.size === 1
          ? t("runtime.serverVersion", { version: [...versions][0] })
          : t("runtime.mixedVersions", { formatted: count(versions.size) }),
    ]
    if (lost > 0) parts.push(t("runtime.lostServers", { formatted: count(lost) }))
    if (disconnected > 0) parts.push(t("runtime.disconnectedServers", { formatted: count(disconnected) }))
    return parts.join(" · ")
  }

  /** 返回最早等待任务已等待的时长说明，没有等待任务时说明无积压。 */
  function waitingDetail() {
    if (!oldestWaitingSince) return t("runtime.noBacklog")
    const seconds = Math.max(0, Math.round((Date.now() - Date.parse(oldestWaitingSince)) / 1000))
    return t("runtime.longestWait", { duration: duration(seconds) })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("runtime.title")} description={t("runtime.description")}>
        <Button variant="outline" size="sm" disabled={exporting} onClick={() => void exportDiagnostics()}>
          {exporting ? t("runtime.exportingDiagnostics") : t("runtime.exportDiagnostics")}
        </Button>
      </PageHeader>

      <ResourceListLayout
        resources={[status, failed, serverErrors]}
        errorMessage={t("runtime.loadError")}
        more={errorTab === "tasks" ? failed.more : serverErrors.more}
        scrollKey={`${location.pathname}?${scrollParams.toString()}`}
      >
        {status.data ? (
          <div className="mb-9 space-y-9">
            <div className="mx-3 grid grid-cols-2 gap-3 lg:grid-cols-4">
              <StatTile label={t("runtime.servers")} value={count(onlineServers.length)} detail={serversDetail()} />
              <StatTile label={t("runtime.waiting")} value={count(waiting)} detail={waitingDetail()} />
              <StatTile
                label={t("runtime.running")}
                value={count(running)}
                detail={t("runtime.pausedDetail", { formatted: count(paused) })}
              />
              <StatTile
                label={t("runtime.failed")}
                value={count(failedCount)}
                detail={t("runtime.retryingDetail", { formatted: count(retrying) })}
              />
            </div>

            <ServerSection servers={servers} />
            <DependencySection status={status.data} onOpenDetail={openDetail} />

            <ReportSection title={t("runtime.queuesTitle")} titleClassName="px-3">
              <ResourceTable<PlatformTaskQueue>
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
                    key: "retrying",
                    header: t("runtime.retrying"),
                    cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
                    cell: (queue) => t("runtime.retryingCell", { formatted: count(queue.retrying) }),
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

        <ReportSection title={t("runtime.recentErrorsTitle")} titleClassName="px-3">
          <Tabs
            value={errorTab}
            onValueChange={(value) => {
              const next = new URLSearchParams(searchParams)
              next.set("errors", value)
              setSearchParams(next, { replace: true })
            }}
          >
            <TabsList className="mx-3">
              <TabsTrigger value="tasks">{t("runtime.failedTasksTitle")}</TabsTrigger>
              <TabsTrigger value="server">{t("runtime.serverErrorsTitle")}</TabsTrigger>
            </TabsList>
            <TabsContent value="tasks">
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
              onRowActivate={(task) =>
                openDetail({
                  title: task.action,
                  mono: true,
                  summary: t("runtime.taskSummary", {
                    workspace: task.workspaceName ?? t("runtime.platformTask"),
                    queue: task.queue,
                    attempts: t("runtime.attempts", { attempt: task.attempt, max: task.maxAttempts }),
                    failedAt: t("runtime.failedAt", { time: formatDateTime(task.failedAt) }),
                  }),
                  error: task.error,
                })
              }
            />
            </TabsContent>
            <TabsContent value="server">
              <ServerErrorTable errors={serverErrors.data?.items ?? []} onOpenDetail={openDetail} />
            </TabsContent>
          </Tabs>
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
                <SheetTitle className={cn("text-base break-all", selected.mono && "font-mono")}>{selected.title}</SheetTitle>
                <SheetDescription>{selected.summary}</SheetDescription>
              </SheetHeader>
              <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-6">
                {selected.fields?.length ? (
                  <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
                    {selected.fields.map(([name, value]) => (
                      <div key={name} className="contents">
                        <dt className="text-muted-foreground">{name}</dt>
                        <dd className="font-mono break-all select-text">{value}</dd>
                      </div>
                    ))}
                  </dl>
                ) : null}
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
    </div>
  )
}

/** 服务端错误列表：出错的业务入口方法或任务、日志消息与主机，第二行为错误信息首行，点击在侧栏查看完整错误与日志属性；没有方法或任务名时以日志消息为标题。 */
function ServerErrorTable({ errors, onOpenDetail }: { errors: PlatformServerError[]; onOpenDetail: (detail: ErrorDetail) => void }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()

  return (
    <ResourceTable<PlatformServerError>
      columns={[
        {
          key: "error",
          header: t("runtime.serverErrorColumn"),
          cellClassName: "min-w-0",
          cell: (record) => (
            <ResourceRowIdentity
              icon={CircleAlertIcon}
              name={record.operation ?? record.action ?? record.message}
              secondary={
                record.operation ?? record.action
                  ? t("runtime.serverErrorIdentity", { message: record.message, hostname: record.hostname })
                  : record.hostname
              }
              description={record.error?.split("\n")[0]}
            />
          ),
        },
        {
          key: "occurredAt",
          header: t("runtime.occurredAtColumn"),
          cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
          cell: (record) => t("runtime.occurredAt", { time: formatDateTime(record.occurredAt) }),
        },
      ]}
      rows={errors}
      rowKey={(record) => record.id}
      empty={t("runtime.noServerErrors")}
      onRowActivate={(record) =>
        onOpenDetail({
          title: record.operation ?? record.action ?? record.message,
          mono: Boolean(record.operation ?? record.action),
          summary:
            record.operation ?? record.action
              ? t("runtime.serverErrorSummary", { message: record.message, time: formatDateTime(record.occurredAt) })
              : t("runtime.occurredAt", { time: formatDateTime(record.occurredAt) }),
          fields: (
            [
              [t("runtime.hostnameField"), record.hostname],
              [t("runtime.instanceField"), record.instanceId.slice(0, 8)],
              [t("runtime.versionField"), record.version],
              [t("runtime.queueColumn"), record.queue],
              [t("runtime.eventIdField"), record.eventId],
              ...Object.entries(record.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b)),
            ] as [string, string | null | undefined][]
          ).filter((field): field is [string, string] => Boolean(field[1])),
          error: record.error ?? "",
        })
      }
    />
  )
}

/** 服务端进程列表：主机名、版本、最近心跳与启动时间，失联或 NATS 未连接时标出。 */
function ServerSection({ servers }: { servers: PlatformServer[] }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()

  /** 返回服务器行的状态标记：失联，或后台任务、实时通知的 NATS 连接断开，正常时不标记。 */
  function serverBadge(server: PlatformServer) {
    if (!server.online) return <StatusBadge variant="destructive">{t("runtime.serverLost")}</StatusBadge>
    if (!server.tasksNatsConnected && !server.realtimeNatsConnected) {
      return <StatusBadge variant="warning">{t("runtime.natsDisconnected")}</StatusBadge>
    }
    if (!server.tasksNatsConnected) return <StatusBadge variant="warning">{t("runtime.tasksNatsDisconnected")}</StatusBadge>
    if (!server.realtimeNatsConnected) return <StatusBadge variant="warning">{t("runtime.realtimeNatsDisconnected")}</StatusBadge>
    return undefined
  }

  return (
    <ReportSection title={t("runtime.serversTitle")} titleClassName="px-3">
      <ResourceTable<PlatformServer>
        columns={[
          {
            key: "server",
            header: t("runtime.serverColumn"),
            cellClassName: "min-w-0",
            cell: (server) => (
              <ResourceRowIdentity
                icon={ServerIcon}
                name={server.hostname}
                secondary={t("runtime.serverIdentity", { version: server.version, id: server.id.slice(0, 8) })}
                badge={serverBadge(server)}
                description={t("runtime.heartbeat", { time: formatDateTime(server.heartbeatAt) })}
              />
            ),
          },
          {
            key: "startedAt",
            header: t("runtime.startedAtColumn"),
            cellClassName: "hidden w-px whitespace-nowrap text-right text-muted-foreground sm:table-cell",
            cell: (server) => t("runtime.startedAt", { time: formatDateTime(server.startedAt) }),
          },
        ]}
        rows={servers}
        rowKey={(server) => server.id}
        empty={t("runtime.noServers")}
      />
    </ReportSection>
  )
}

/** 外部依赖列表：对象存储与授权服务的状态，异常的依赖可点击查看完整错误。 */
function DependencySection({ status, onOpenDetail }: { status: PlatformRuntimeStatus; onOpenDetail: (detail: ErrorDetail) => void }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()

  /** 按对象存储与授权服务的状态生成外部依赖列表，异常的依赖附带错误详情。 */
  function dependencyRows({ objectStorage: storage, control }: PlatformRuntimeStatus): DependencyRow[] {
    const storageName = t("runtime.objectStorage")
    const controlName = t("runtime.control")
    const healthy = <StatusBadge variant="success">{t("runtime.healthy")}</StatusBadge>
    let storageRow: DependencyRow = {
      key: "storage", icon: CloudIcon, name: storageName, badge: healthy, description: t("runtime.storageHealthyDetail"), detail: null,
    }
    if (!storage.enabled) {
      storageRow = {
        ...storageRow,
        badge: <StatusBadge variant="muted">{t("runtime.storageLocal")}</StatusBadge>,
        description: t("runtime.storageLocalDetail"),
      }
    } else if (storage.error) {
      storageRow = {
        ...storageRow,
        badge: <StatusBadge variant="destructive">{t("runtime.checkFailed")}</StatusBadge>,
        description: storage.error,
        detail: { title: storageName, summary: t("runtime.storageErrorSummary"), error: storage.error },
      }
    }
    let controlRow: DependencyRow = {
      key: "control", icon: KeyRoundIcon, name: controlName,
      badge: <StatusBadge variant="muted">{t("runtime.notSynced")}</StatusBadge>, description: t("runtime.notSyncedDetail"), detail: null,
    }
    if (control.failedAt) {
      const failed = formatDateTime(control.failedAt)
      controlRow = {
        ...controlRow,
        badge: <StatusBadge variant="destructive">{t("runtime.syncFailed")}</StatusBadge>,
        description: t("runtime.controlFailedDetail", { time: failed, error: control.error }),
        detail: {
          title: controlName,
          summary: control.syncedAt
            ? t("runtime.controlFailedSummary", { failed, synced: formatDateTime(control.syncedAt) })
            : t("runtime.controlNeverSyncedSummary", { failed }),
          error: control.error,
        },
      }
    } else if (control.syncedAt) {
      controlRow = { ...controlRow, badge: healthy, description: t("runtime.controlSyncedDetail", { time: formatDateTime(control.syncedAt) }) }
    }
    return [storageRow, controlRow]
  }

  return (
    <ReportSection title={t("runtime.dependenciesTitle")} titleClassName="px-3">
      <ResourceTable<DependencyRow>
        columns={[
          {
            key: "dependency",
            header: t("runtime.dependencyColumn"),
            cellClassName: "min-w-0",
            cell: (row) => (
              <ResourceRowIdentity icon={row.icon} name={row.name} badge={row.badge} description={row.description} />
            ),
          },
        ]}
        rows={dependencyRows(status)}
        rowKey={(row) => row.key}
        empty={null}
        canActivateRow={(row) => row.detail !== null}
        onRowActivate={(row) => (row.detail ? onOpenDetail(row.detail) : undefined)}
      />
    </ReportSection>
  )
}
