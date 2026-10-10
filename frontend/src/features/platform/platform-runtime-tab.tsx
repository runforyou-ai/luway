/** 概览的运行状态页签：各服务端进程的版本、心跳与消息总线负载，以及后台任务各队列的等待、执行、重试与失败情况，页头导出诊断信息。 */
import { ServerIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { getPlatformRuntimeStatus, type PlatformServer, type PlatformTaskQueue } from "@/api"
import { ReportSection, StatTile } from "@/components/report-parts"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { StatusBadge } from "@/components/status-badge"
import { DiagnosticsExportButton } from "@/features/platform/platform-diagnostics-export"
import { PlatformTabsActions } from "@/features/platform/platform-tabs"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useReportFormat } from "@/hooks/use-report-format"
import { useResource } from "@/hooks/use-resource"

/** 运行状态与近期错误的自动刷新间隔。 */
export const runtimeRefreshInterval = 15_000

/** 消息总线传输驱动的显示名称。 */
const busDriverNames: Record<string, string> = { postgres: "PostgreSQL", nats: "NATS" }

/** 经 PostgreSQL 传输时建议改用 NATS 的每秒送达次数，按各服务器发送速率之和乘以在线服务器数计算。 */
const busDeliveryAdvisory = 100_000

/** 提示通知队列积压的使用比例。 */
const notifyQueueAdvisory = 0.01

/** 展示服务端进程与后台任务运行状态，每隔一段时间自动刷新。 */
export function PlatformRuntimeTab() {
  const { t } = useTranslation("platform")
  const { count, percent } = useReportFormat()
  const status = useResource(resourceKeys.platformRuntime(), (signal) => getPlatformRuntimeStatus(signal), {
    staleTime: 0,
    refetchInterval: runtimeRefreshInterval,
  })
  const queues = status.data?.queues ?? []
  const waiting = queues.reduce((sum, queue) => sum + queue.waiting, 0)
  const running = queues.reduce((sum, queue) => sum + queue.running, 0)
  const failedCount = queues.reduce((sum, queue) => sum + queue.failed, 0)
  const servers = status.data?.servers ?? []
  const onlineServers = servers.filter((server) => server.online)
  const versions = new Set(onlineServers.map((server) => server.version))
  const busDrivers = new Set(onlineServers.map((server) => server.busDriver))
  const busRate = onlineServers.reduce((sum, server) => sum + server.busMessageRate, 0)
  const notifyQueueUsage = status.data?.notifyQueueUsage ?? 0

  /** 返回在线服务器的版本说明，并附上消息总线传输方式不一致、失联与消息总线异常的服务器数。 */
  function serversDetail() {
    const lost = servers.length - onlineServers.length
    const disconnected = onlineServers.filter((server) => !server.busConnected).length
    const parts = [
      versions.size === 0
        ? t("runtime.noServers")
        : versions.size === 1
          ? t("runtime.serverVersion", { version: [...versions][0] })
          : t("runtime.mixedVersions", { formatted: count(versions.size) }),
    ]
    if (busDrivers.size > 1) parts.push(t("runtime.mixedBusDrivers"))
    if (lost > 0) parts.push(t("runtime.lostServers", { formatted: count(lost) }))
    if (disconnected > 0) parts.push(t("runtime.disconnectedServers", { formatted: count(disconnected) }))
    return parts.join(" · ")
  }

  /** 返回消息总线的传输方式与通知队列使用比例，并附上丢失消息的服务器数、通知队列积压与改用 NATS 的建议。 */
  function busDetail() {
    const dropping = onlineServers.filter((server) => server.busFailures > 0).length
    const postgres = busDrivers.has("postgres")
    const parts = [...busDrivers].map((driver) => busDriverNames[driver] ?? driver)
    if (dropping > 0) parts.push(t("runtime.busDroppingServers", { formatted: count(dropping) }))
    parts.push(
      notifyQueueUsage >= notifyQueueAdvisory
        ? t("runtime.notifyQueueBacklog", { percent: percent(notifyQueueUsage) })
        : t("runtime.notifyQueue", { percent: percent(notifyQueueUsage) }),
    )
    if (postgres && busRate * onlineServers.length >= busDeliveryAdvisory) parts.push(t("runtime.busAdvisory"))
    return parts.join(" · ")
  }

  return (
    <>
      <PlatformTabsActions>
        <DiagnosticsExportButton />
      </PlatformTabsActions>
      <ResourceListLayout resources={status} errorMessage={t("runtime.loadError")}>
        {status.data ? (
          <div className="space-y-9">
            <div className="mx-3 grid grid-cols-2 gap-3 lg:grid-cols-5">
              <StatTile label={t("runtime.servers")} value={count(onlineServers.length)} detail={serversDetail()} />
              <StatTile
                label={t("runtime.busColumn")}
                value={t("runtime.busRate", { formatted: count(Math.round(busRate)) })}
                detail={busDetail()}
              />
              <StatTile
                label={t("runtime.waiting")}
                value={count(waiting)}
                detail={t("runtime.delayedDetail", { formatted: count(status.data.delayedTasks) })}
              />
              <StatTile label={t("runtime.running")} value={count(running)} detail={t("runtime.runningDetail")} />
              <StatTile label={t("runtime.failed")} value={count(failedCount)} detail={t("runtime.failedDetail")} />
            </div>

            <ServerSection servers={servers} />

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
      </ResourceListLayout>
    </>
  )
}

/** 服务端进程列表：主机名、版本、最近心跳、消息总线传输方式与发送速率、启动时间，失联、消息总线未连接或丢失消息时标出。 */
function ServerSection({ servers }: { servers: PlatformServer[] }) {
  const { t } = useTranslation("platform")
  const { formatDateTime } = useDateTime()
  const { count } = useReportFormat()

  /** 返回服务器行的状态标记：失联、消息总线未连接或消息总线丢失消息，正常时不标记。 */
  function serverBadge(server: PlatformServer) {
    if (!server.online) return <StatusBadge variant="destructive">{t("runtime.serverLost")}</StatusBadge>
    if (!server.busConnected) return <StatusBadge variant="warning">{t("runtime.busDisconnected")}</StatusBadge>
    if (server.busFailures > 0) return <StatusBadge variant="warning">{t("runtime.busDropping")}</StatusBadge>
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
            key: "bus",
            header: t("runtime.busColumn"),
            cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground",
            cell: (server) =>
              t("runtime.busCell", {
                driver: busDriverNames[server.busDriver] ?? server.busDriver,
                rate: t("runtime.busRate", { formatted: count(Math.round(server.busMessageRate)) }),
              }),
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
