/** 页面内容区的左右分栏。 */
import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { NavLink, useLocation } from "react-router"

import { CountBadge, type CountBadgeTone } from "@/components/count-badge"
import { StatusBadge } from "@/components/status-badge"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

const paneOnNarrowClass = {
  hide: "hidden md:flex",
  fill: "flex w-full",
} as const

type PageSplitPaneOnNarrow = keyof typeof paneOnNarrowClass

/** 分割左栏和主区；宽屏下左栏即模块中栏，消息页会话列表与通讯录、AI 员工二级菜单统一 280px。 */
export function PageSplit({
  pane,
  paneOnNarrow = "hide",
  paneVariant = "plain",
  paneClassName,
  mainClassName,
  className,
  children,
}: {
  pane: ReactNode
  paneOnNarrow?: PageSplitPaneOnNarrow
  paneVariant?: "plain" | "nav"
  paneClassName?: string
  mainClassName?: string
  className?: string
  children: ReactNode
}) {
  return (
    <div
      data-slot="page-split"
      // 左栏与主区在宽屏下是两张各自带圆角的卡片，中间的缝透明并透出工作台底色。
      className={cn(
        "flex min-h-0 w-full flex-1 overflow-hidden md:gap-2",
        className,
      )}
    >
      <aside
        data-slot="page-split-pane"
        className={cn(
          "min-h-0 shrink-0 flex-col overflow-hidden bg-background select-none md:rounded-xl",
          paneOnNarrowClass[paneOnNarrow],
          "md:w-70",
          paneVariant === "nav" &&
            "bg-sidebar-secondary text-sidebar-foreground",
          paneClassName,
        )}
      >
        {pane}
      </aside>
      <div
        data-slot="page-split-main"
        className={cn(
          "flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background md:rounded-xl",
          mainClassName,
        )}
      >
        {children}
      </div>
    </div>
  )
}

/** 分栏左栏导航列表。 */
export function PagePaneNav({
  label,
  title,
  action,
  children,
}: {
  label: string
  title?: string
  action?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {title ? (
        // 标题距中间栏顶边固定 20px；操作按钮只在标题行高度内垂直居中。
        <div className="flex shrink-0 items-start gap-2 px-3.5 pt-5 pb-1.5">
          <h2 className="min-w-0 flex-1 truncate text-lg font-semibold tracking-tight">
            {title}
          </h2>
          {action ? (
            <div className="flex h-6 shrink-0 items-center gap-2">{action}</div>
          ) : null}
        </div>
      ) : null}
      <ScrollArea className="min-h-0 flex-1">
        <nav className="flex flex-col gap-0.5 p-2.5" aria-label={label}>
          {children}
        </nav>
      </ScrollArea>
    </div>
  )
}

/** 分栏左栏和工作台一级栏的导航项；activePath 按一个或多个公共路径前缀保持整组页面的选中态，count 大于零时在行尾显示数量，窄栏下只显示图标并由浮层提示名称。 */
export function PagePaneLink({
  to,
  activePath,
  icon: Icon,
  collapsed,
  comingSoonHint = true,
  count = 0,
  countLabel,
  countTone = "alert",
  className: itemClassName,
  onClick,
  children,
}: {
  to?: string
  activePath?: string | readonly string[]
  icon?: LucideIcon
  collapsed?: boolean
  /** 未开放项是否显示「即将推出」标签和悬停提示；关闭时只置灰。 */
  comingSoonHint?: boolean
  count?: number
  /** 数量的无障碍说明。 */
  countLabel?: string
  /** 数量徽标配色。 */
  countTone?: CountBadgeTone
  className?: string
  onClick?: () => void
  children: ReactNode
}) {
  const { t } = useTranslation("common")
  const { pathname } = useLocation()
  const prefixActive = (
    typeof activePath === "string" ? [activePath] : (activePath ?? [])
  ).some((path) => pathname === path || pathname.startsWith(`${path}/`))
  // 选中态按当前路径判定，className 保持字符串形式，供窄栏下的浮层触发器合并。
  const active =
    prefixActive ||
    (to !== undefined && (pathname === to || pathname.startsWith(`${to}/`)))
  const className = cn(
    "flex h-8 shrink-0 items-center rounded-md text-left text-sm transition-colors",
    collapsed ? "relative w-8 justify-center" : "w-full gap-2 px-2.5",
    itemClassName,
  )
  const label = collapsed ? (
    <span className="sr-only">{children}</span>
  ) : (
    <span className="min-w-0 flex-1 truncate">{children}</span>
  )
  // 展开时行尾显示数量，窄栏下在图标角上显示圆点。
  const badge = count > 0 ? (
    collapsed ? (
      <span
        aria-label={countLabel}
        className={cn(
          "absolute top-1 right-1 size-1.5 rounded-full",
          countTone === "neutral" ? "bg-foreground/50" : "bg-destructive",
        )}
      />
    ) : (
      <CountBadge count={count} label={countLabel} tone={countTone} />
    )
  ) : null

  const item = to ? (
    <NavLink
      to={to}
      onClick={onClick}
      className={cn(
        className,
        "hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring",
        active && "bg-sidebar-accent font-medium text-sidebar-accent-foreground",
      )}
    >
      {Icon ? <Icon className="size-4 shrink-0" /> : null}
      {label}
      {badge}
    </NavLink>
  ) : (
    <span
      className={cn(className, "cursor-default text-muted-foreground")}
      aria-disabled="true"
      title={collapsed || !comingSoonHint ? undefined : t("comingSoon")}
    >
      {Icon ? <Icon className="size-4 shrink-0" /> : null}
      {label}
      {collapsed || !comingSoonHint ? null : (
        <StatusBadge variant="muted">{t("comingSoon")}</StatusBadge>
      )}
    </span>
  )

  if (!collapsed) {
    return item
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>{item}</TooltipTrigger>
      <TooltipContent side="right">
        {children}
        {to ? null : ` · ${t("comingSoon")}`}
      </TooltipContent>
    </Tooltip>
  )
}

/** 分栏左栏导航分组，分组标题下是同组导航项；窄栏下标题由分隔线代替。 */
export function PagePaneGroup({
  title,
  collapsed,
  children,
}: {
  title: string
  collapsed?: boolean
  children: ReactNode
}) {
  if (collapsed) {
    return (
      <div className="mt-1.5 flex flex-col items-center gap-1.5 border-t border-sidebar-border pt-1.5">
        {children}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-0.5">
      <span className="px-2.5 pt-2.5 pb-0.5 text-xs font-semibold tracking-[0.12em] text-muted-foreground/70 uppercase">
        {title}
      </span>
      {children}
    </div>
  )
}
