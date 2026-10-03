/** 报表共用的统计周期筛选、指标卡、分区、条形列表、逐日柱状图与空状态说明。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { ListToolbarFilter } from "@/components/list-toolbar"
import { periodOptions } from "@/hooks/use-report-format"
import { cn } from "@/lib/utils"

/** 按结束时间筛选的统计周期。 */
export function ReportPeriodFilter({ value, onValueChange }: { value: number; onValueChange: (value: string) => void }) {
  const { t } = useTranslation("common")
  return (
    <ListToolbarFilter
      label={t("report.period")}
      value={String(value)}
      options={periodOptions.map((option) => ({
        value: String(option),
        label: t("report.periodDays", { count: option }),
      }))}
      onValueChange={onValueChange}
    />
  )
}

/** 指标卡：名称、主数值与一行说明；给出 onClick 时整卡可点击。 */
export function StatTile({
  label,
  value,
  detail,
  onClick,
}: {
  label: string
  value: string
  detail: string
  onClick?: () => void
}) {
  const content = (
    <>
      <p className="truncate text-sm text-muted-foreground">{label}</p>
      <p className="mt-1.5 text-2xl font-semibold tabular-nums">{value}</p>
      <p className="mt-1 truncate text-xs text-muted-foreground">{detail}</p>
    </>
  )
  const className = "rounded-lg border border-border/55 px-4 py-3.5 text-left"
  return onClick ? (
    <button
      type="button"
      className={cn(
        className,
        "transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
      )}
      onClick={onClick}
    >
      {content}
    </button>
  ) : (
    <div className={className}>{content}</div>
  )
}

/** 概览分区：小标题与内容；titleClassName 用于在列表外框中对齐标题。 */
export function ReportSection({ title, titleClassName, children }: { title: string; titleClassName?: string; children: ReactNode }) {
  return (
    <section className="space-y-3">
      <h3 className={cn("text-sm font-medium", titleClassName)}>{title}</h3>
      {children}
    </section>
  )
}

/** 按占总数比例绘制的横向条形列表，数值以文字写在行尾；行给出 total 时按自身总数计算，给出 onSelect 时整行可点击。 */
export function MeterList({
  rows,
  total = 0,
  format,
}: {
  rows: { key: string; label: string; value: number; total?: number; onSelect?: () => void }[]
  total?: number
  format: (value: number, total: number) => string
}) {
  return (
    <ul className="space-y-2.5">
      {rows.map((row) => {
        const rowTotal = row.total ?? total
        const content = (
          <>
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="truncate">{row.label}</span>
              <span className="shrink-0 text-muted-foreground tabular-nums">
                {format(row.value, rowTotal)}
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary"
                style={{ width: `${rowTotal > 0 ? (row.value / rowTotal) * 100 : 0}%` }}
              />
            </div>
          </>
        )
        return (
          <li key={row.key} title={`${row.label} ${format(row.value, rowTotal)}`}>
            {row.onSelect ? (
              <button
                type="button"
                className="-mx-2 -my-1 block w-[calc(100%+1rem)] space-y-1 rounded-md px-2 py-1 text-left transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
                onClick={row.onSelect}
              >
                {content}
              </button>
            ) : (
              <div className="space-y-1">{content}</div>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/** 分区内没有数据时的说明。 */
export function EmptyNote({ children }: { children: ReactNode }) {
  return <p className="py-6 text-center text-sm text-muted-foreground">{children}</p>
}

/** 逐日柱状图：每天一根柱，柱高按区间最大值缩放，悬停显示当天说明；下方标出首尾日期，并为读屏提供逐日数据表。 */
export function DailyBarChart({
  label,
  days,
}: {
  label: string
  days: { key: string; label: string; value: number; detail: string }[]
}) {
  const max = Math.max(1, ...days.map((day) => day.value))
  return (
    <figure aria-label={label}>
      <div className="flex h-32 items-end gap-0.5 border-b border-border/55" aria-hidden="true">
        {days.map((day) => (
          <div key={day.key} className="group flex h-full min-w-0 flex-1 items-end" title={day.detail}>
            <div
              className="w-full rounded-t-[4px] bg-primary/75 transition-colors group-hover:bg-primary"
              style={{ height: `${(day.value / max) * 100}%`, minHeight: day.value > 0 ? 2 : 0 }}
            />
          </div>
        ))}
      </div>
      {days.length > 0 ? (
        <div className="mt-1.5 flex justify-between text-xs text-muted-foreground tabular-nums" aria-hidden="true">
          <span>{days[0].label}</span>
          <span>{days[days.length - 1].label}</span>
        </div>
      ) : null}
      <table className="sr-only">
        <caption>{label}</caption>
        <tbody>
          {days.map((day) => (
            <tr key={day.key}>
              <td>{day.detail}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  )
}
