/** 概览的日志页签：按级别、服务器、串联编号、工作区与业务入口筛选近 30 天的服务端日志，滚动加载更早的日志，页头刷新读取最新日志，点击在侧栏查看详情并可按其中的取值继续筛选。 */
import { BugIcon, CircleAlertIcon, InfoIcon, ListFilterIcon, TriangleAlertIcon, type LucideIcon } from "lucide-react"
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  getPlatformRuntimeStatus,
  listPlatformServerLogs,
  ServerLogLevel,
  type PlatformServerLog,
} from "@/api"
import { ListToolbar, ListToolbarFilter, ListToolbarReset, ListToolbarSearch } from "@/components/list-toolbar"
import { ResourceListLayout } from "@/components/resource-list"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { PlatformTabsActions } from "@/features/platform/platform-tabs"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useListSearchParams } from "@/hooks/use-list-search-params"
import { useCursorResource, useResource } from "@/hooks/use-resource"
import { cn } from "@/lib/utils"

/** 各级别的图标与图标颜色。 */
const levelDisplay: Record<ServerLogLevel, { icon: LucideIcon; className: string }> = {
  [ServerLogLevel.Debug]: { icon: BugIcon, className: "text-muted-foreground" },
  [ServerLogLevel.Info]: { icon: InfoIcon, className: "text-muted-foreground" },
  [ServerLogLevel.Warn]: { icon: TriangleAlertIcon, className: "text-warning" },
  [ServerLogLevel.Error]: { icon: CircleAlertIcon, className: "text-destructive" },
}

/** 最低级别筛选的选项，从低到高排列；不选表示全部级别。 */
const minLevels = [ServerLogLevel.Info, ServerLogLevel.Warn, ServerLogLevel.Error] as const

/** 侧栏中可点击筛选的字段对应的地址参数。 */
type FilterParameter = "server" | "workspace" | "entry" | "q"

/** 按地址参数筛选并列出服务端日志，侧栏展示所选日志的详情。 */
export function PlatformLogListTab() {
  const { t } = useTranslation(["platform", "common"])
  const { formatDateTimeSeconds } = useDateTime()
  const { searchParams, setParameters, query, search, setSearch } = useListSearchParams()
  const minLevel = minLevels.find((value) => value === searchParams.get("level"))
  const instanceId = searchParams.get("server") ?? ""
  const workspaceId = searchParams.get("workspace") ?? ""
  const entry = searchParams.get("entry") ?? ""
  const traceId = query.trim()
  const [selected, setSelected] = useState<PlatformServerLog | null>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const runtime = useResource(resourceKeys.platformRuntime(), (signal) => getPlatformRuntimeStatus(signal), { staleTime: 0 })
  const filters = { minLevel, instanceId, workspaceId, entry, traceId }
  const logs = useCursorResource(
    resourceKeys.platformServerLogs(filters),
    (cursor, signal) => listPlatformServerLogs({ ...filters, cursor }, signal),
    { select: (data) => ({ items: data.logs, nextCursor: data.nextCursor }), itemKey: (item) => item.id, keepPreviousData: true, staleTime: 0 },
  )
  const servers = runtime.data?.servers ?? []
  const workspaceName = logs.data?.items.find((item) => item.workspaceId === workspaceId)?.workspaceName

  /** 按侧栏中的取值筛选并关闭侧栏。 */
  function applyFilter(parameter: FilterParameter, value: string) {
    setSelected(null)
    if (parameter === "q") setSearch(value)
    else setParameters({ [parameter]: value }, true)
  }

  return (
    <>
      <PlatformTabsActions>
        <Button variant="outline" size="sm" disabled={logs.refreshing} onClick={() => void logs.refresh()}>
          {t("common:actions.refresh")}
        </Button>
      </PlatformTabsActions>
      <ListToolbar>
        <ListToolbarSearch value={search} aria-label={t("logs.traceSearch")} onChange={(event) => setSearch(event.target.value)} />
        <ListToolbarFilter
          label={t("logs.level")}
          allLabel={t("logs.allLevels")}
          value={minLevel}
          options={minLevels.map((value) => ({ value, label: t(`logs.minLevels.${value}`) }))}
          onValueChange={(next) => setParameters({ level: next }, true)}
        />
        <ListToolbarFilter
          label={t("logs.server")}
          allLabel={t("logs.allServers")}
          value={instanceId}
          options={[
            ...servers.map((server) => ({ value: server.id, label: t("logs.serverOption", { hostname: server.hostname, id: server.id.slice(0, 8) }) })),
            ...(instanceId && !servers.some((server) => server.id === instanceId) ? [{ value: instanceId, label: instanceId.slice(0, 8) }] : []),
          ]}
          onValueChange={(next) => setParameters({ server: next }, true)}
        />
        {workspaceId ? (
          <ListToolbarFilter
            label={t("logs.workspace")}
            allLabel={t("logs.allWorkspaces")}
            value={workspaceId}
            options={[{ value: workspaceId, label: workspaceName ?? workspaceId.slice(0, 8) }]}
            onValueChange={(next) => setParameters({ workspace: next }, true)}
          />
        ) : null}
        {entry ? (
          <ListToolbarFilter
            label={t("logs.entry")}
            allLabel={t("logs.allEntries")}
            value={entry}
            options={[{ value: entry, label: entry }]}
            onValueChange={(next) => setParameters({ entry: next }, true)}
          />
        ) : null}
        {minLevel || instanceId || workspaceId || entry || traceId ? (
          <ListToolbarReset
            onClick={() => {
              setSearch("")
              setParameters({ level: null, server: null, workspace: null, entry: null, q: null }, true)
            }}
          >
            {t("common:actions.clearFilters")}
          </ListToolbarReset>
        ) : null}
      </ListToolbar>
      <ResourceListLayout resources={logs} errorMessage={t("logs.loadError")} more={logs.more}>
        <ResourceTable<PlatformServerLog>
          columns={[
            {
              key: "log",
              header: t("logs.messageColumn"),
              cellClassName: "min-w-0",
              cell: (record) => {
                const display = levelDisplay[record.level]
                return (
                  <ResourceRowIdentity
                    leading={<display.icon aria-label={t(`logs.levels.${record.level}`)} className={cn("size-4 shrink-0", display.className)} />}
                    name={record.message}
                    secondary={[record.operation ?? record.action, record.workspaceName, record.hostname].filter(Boolean).join(" · ")}
                    description={record.error?.split("\n")[0]}
                  />
                )
              },
            },
            {
              key: "occurredAt",
              header: t("logs.occurredAtColumn"),
              cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
              cell: (record) => formatDateTimeSeconds(record.occurredAt),
            },
          ]}
          rows={logs.data?.items ?? []}
          rowKey={(record) => record.id}
          empty={t("logs.empty")}
          onRowActivate={(record) => {
            trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
            setSelected(record)
          }}
        />
      </ResourceListLayout>
      <ServerLogSheet record={selected} trigger={trigger} onClose={() => setSelected(null)} onFilter={applyFilter} />
    </>
  )
}

/** 日志详情侧栏：作用域字段与日志属性逐项列出，串联编号、业务入口、工作区与服务器可点击按其取值筛选，最后展示完整错误信息。 */
function ServerLogSheet({
  record,
  trigger,
  onClose,
  onFilter,
}: {
  record: PlatformServerLog | null
  trigger: { current: HTMLElement | null }
  onClose: () => void
  onFilter: (parameter: FilterParameter, value: string) => void
}) {
  const { t } = useTranslation("platform")
  const { formatDateTimeSeconds } = useDateTime()
  const entry = record?.operation ?? record?.action
  const fields: [string, string | null | undefined, FilterParameter?][] = record
    ? [
        [t("logs.traceField"), record.traceId, "q"],
        [t("logs.entryField"), entry, "entry"],
        [t("logs.workspaceField"), record.workspaceId ? (record.workspaceName ?? record.workspaceId) : null, "workspace"],
        [t("logs.serverField"), t("logs.serverOption", { hostname: record.hostname, id: record.instanceId.slice(0, 8) }), "server"],
        [t("logs.versionField"), record.version],
        [t("logs.taskRunField"), record.taskRunId],
        [t("logs.queueField"), record.queue],
        [t("logs.accountField"), record.accountId],
        [t("logs.eventIdField"), record.eventId],
        ...Object.entries(record.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b)),
      ]
    : []
  // 可筛选字段的筛选取值：工作区与服务器按编号筛选，其余按显示的取值筛选。
  const filterValue = (parameter: FilterParameter, shown: string) =>
    parameter === "workspace" ? (record?.workspaceId ?? "") : parameter === "server" ? (record?.instanceId ?? "") : shown

  return (
    <Sheet open={record !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent
        className="w-full gap-0 p-0 sm:max-w-xl"
        onCloseAutoFocus={(event) => {
          if (!trigger.current?.isConnected) return
          event.preventDefault()
          trigger.current.focus({ preventScroll: true })
        }}
      >
        {record ? (
          <>
            <SheetHeader className="border-b px-6 py-4 pr-12">
              <SheetTitle className="text-base break-all">{record.message}</SheetTitle>
              <SheetDescription>
                {t("logs.summary", { level: t(`logs.levels.${record.level}`), time: formatDateTimeSeconds(record.occurredAt) })}
              </SheetDescription>
            </SheetHeader>
            <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-6">
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2 text-sm">
                {fields
                  .filter((field): field is [string, string, FilterParameter?] => Boolean(field[1]))
                  .map(([name, value, parameter]) => (
                    <div key={name} className="contents">
                      <dt className="text-muted-foreground">{name}</dt>
                      <dd className="flex min-w-0 items-center gap-1">
                        <span className="min-w-0 font-mono break-all select-text">{value}</span>
                        {parameter ? (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7 shrink-0 text-muted-foreground"
                            aria-label={t("logs.filterBy", { field: name })}
                            title={t("logs.filterBy", { field: name })}
                            onClick={() => onFilter(parameter, filterValue(parameter, value))}
                          >
                            <ListFilterIcon />
                          </Button>
                        ) : null}
                      </dd>
                    </div>
                  ))}
              </dl>
              {record.error ? (
                <div>
                  <h3 className="mb-2 text-sm font-medium">{t("logs.errorTitle")}</h3>
                  <pre className="font-mono text-sm break-all whitespace-pre-wrap text-muted-foreground select-text">{record.error}</pre>
                </div>
              ) : null}
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
