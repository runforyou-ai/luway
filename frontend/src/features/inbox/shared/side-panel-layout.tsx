/** 会话侧边面板共用的分段页签和资料字段行。 */
import type { ComponentProps, ComponentType, ReactNode } from "react"

import { TabsList, TabsTrigger } from "@/components/ui/tabs"
import { FieldRequiredMark } from "@/components/ui/field"
import { cn } from "@/lib/utils"

/** 资料字段行组件，需放在 dl 内。 */
export type ProfileField = ComponentType<{
  label: string
  children: ReactNode
}>

/** 资料分组组件，标题下的字段行放在 dl 内。 */
export type ProfileSection = ComponentType<{
  title: string
  children: ReactNode
}>

/** 侧边面板资料分组：小标题与字段列表，样式与历史咨询一致。 */
export function SidePanelSection({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <section className="mt-5 space-y-2">
      <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
      <dl className="space-y-1 text-sm">{children}</dl>
    </section>
  )
}

/** 侧边面板顶部的分段页签列表，右端为收起按钮留出位置。 */
export function SidePanelTabsList({
  className,
  ...props
}: ComponentProps<typeof TabsList>) {
  return (
    <TabsList
      className={cn(
        "h-auto min-h-12 shrink-0 justify-start gap-1 border-b-0 px-3 py-2",
        className,
      )}
      {...props}
    />
  )
}

/** 侧边面板分段页签，样式与会话列表的范围页签一致。 */
export function SidePanelTab({
  className,
  ...props
}: ComponentProps<typeof TabsTrigger>) {
  return (
    <TabsTrigger
      className={cn(
        "-mb-0 flex h-7 items-center rounded-md border-b-0 px-2 py-0 font-normal hover:bg-muted data-[state=active]:bg-accent data-[state=active]:font-medium data-[state=active]:text-accent-foreground data-[state=active]:hover:bg-accent data-[state=active]:hover:text-accent-foreground",
        className,
      )}
      {...props}
    />
  )
}

/** 侧边面板资料字段行，需放在 dl 内；给出 action 时在行尾增加操作列。 */
export function SidePanelField({
  label,
  required = false,
  action,
  className,
  children,
}: {
  label: string
  required?: boolean
  action?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <div
      className={cn(
        "grid items-start gap-2",
        action === undefined
          ? "grid-cols-[4.75rem_minmax(0,1fr)]"
          : "group grid-cols-[4.75rem_minmax(0,1fr)_auto]",
        className,
      )}
    >
      <dt className="flex min-h-7 min-w-0 items-center gap-1 text-xs text-muted-foreground">
        <span className="min-w-0 truncate" title={label}>
          {label}
        </span>
        {required ? <FieldRequiredMark /> : null}
      </dt>
      <dd className="flex min-h-7 min-w-0 items-center gap-2">{children}</dd>
      {action === undefined ? null : (
        <div className="flex min-h-7 items-center">{action}</div>
      )}
    </div>
  )
}
