/** 列表页工具栏、搜索框和筛选器。 */
import type { InputHTMLAttributes, ReactNode } from "react"
import { CheckIcon, ChevronDownIcon, SearchIcon, XIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

type ListToolbarOption = {
  value: string
  label: string
}

/** 列表工具栏容器。 */
export function ListToolbar({ children }: { children: ReactNode }) {
  return (
    <div className="app-page-gutter flex flex-wrap items-center gap-2 py-2 select-none">
      {children}
    </div>
  )
}

/** 列表搜索输入框；底色和圆角与表单输入框一致，高度按工具栏密度取 36px。 */
export function ListToolbarSearch({
  className,
  ...props
}: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <div className={cn("relative w-full sm:w-64", className)}>
      <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        {...props}
        className="h-9 rounded-lg bg-muted/60 pl-9 dark:bg-muted/50"
      />
    </div>
  )
}

/** 列表筛选下拉；可清空的筛选选中后高亮并提供移除入口。 */
export function ListToolbarFilter({
  label,
  allLabel,
  value,
  options,
  onValueChange,
  align = "start",
  contentClassName,
}: {
  label: string
  allLabel?: string
  value: string
  options: ListToolbarOption[]
  onValueChange: (value: string) => void
  align?: "start" | "center" | "end"
  contentClassName?: string
}) {
  const { t } = useTranslation("common")
  const selected = options.find((option) => option.value === value)
  // 没有「全部」选项的筛选始终带值，作为视图切换保持中性样式。
  const active = Boolean(allLabel) && Boolean(value)

  return (
    <div
      className={cn(
        "inline-flex h-9 items-center rounded-lg border text-sm transition-colors",
        active
          ? "border-transparent bg-accent text-accent-foreground"
          : "border-input bg-muted/60 hover:bg-accent hover:text-accent-foreground dark:bg-muted/50",
      )}
    >
      <DropdownMenu>
        <DropdownMenuTrigger
          className={cn(
            "inline-flex h-full items-center gap-1.5 rounded-lg px-3 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
            active && "pr-1.5",
          )}
        >
          <span className={active ? "text-accent-foreground/70" : "text-muted-foreground"}>
            {label}
          </span>
          {selected ? <span className="font-medium">{selected.label}</span> : null}
          {active ? null : (
            <ChevronDownIcon className="size-3 text-muted-foreground" />
          )}
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align={align}
          className={cn("min-w-44", contentClassName)}
        >
          <DropdownMenuLabel>{label}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          {allLabel ? (
            <DropdownMenuItem onSelect={() => onValueChange("")}>
              <CheckIcon className={cn(!value && "opacity-100", value && "opacity-0")} />
              {allLabel}
            </DropdownMenuItem>
          ) : null}
          {options.map((option) => (
            <DropdownMenuItem
              key={option.value}
              onSelect={() => onValueChange(option.value)}
            >
              <CheckIcon className={cn(value === option.value ? "opacity-100" : "opacity-0")} />
              {option.label}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      {active ? (
        <button
          type="button"
          aria-label={`${t("actions.remove")} ${label}`}
          title={`${t("actions.remove")} ${label}`}
          className="mr-2 inline-flex size-4 items-center justify-center rounded-full text-accent-foreground/70 transition-colors hover:bg-accent-foreground/10 hover:text-accent-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
          onClick={() => onValueChange("")}
        >
          <XIcon className="size-3" />
        </button>
      ) : null}
    </div>
  )
}

/** 清除列表筛选。 */
export function ListToolbarReset({
  children,
  onClick,
}: {
  children: ReactNode
  onClick: () => void
}) {
  return (
    <Button variant="ghost" className="h-9 rounded-lg" onClick={onClick}>
      {children}
    </Button>
  )
}

/** 工具栏末尾的列表总数；children 替换默认的「共 N 条」文案，数量未知时不渲染。 */
export function ListToolbarTotal({
  count,
  children,
}: {
  count: number | undefined
  children?: ReactNode
}) {
  const { t } = useTranslation("common")
  if (count === undefined) return null
  return (
    <span className="ml-auto text-xs text-muted-foreground tabular-nums">
      {children ?? t("pagination.total", { count })}
    </span>
  )
}
