/** 客服报表概览共用的指标卡、分区、条形列表与空状态说明。 */
import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

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

/** 概览分区：小标题与内容。 */
export function ReportSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-3">
      <h3 className="text-sm font-medium">{title}</h3>
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
