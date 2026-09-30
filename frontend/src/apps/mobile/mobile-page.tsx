/** 移动端统一标题、列表滚动区域和页面状态。 */
import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react"
import { ArrowLeftIcon, SearchIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  useMobileBack,
  useMobileNavigation,
} from "@/apps/mobile/mobile-navigation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

/** 居中显示移动端标题，两侧保留等宽的返回和操作空间。 */
export function MobilePageHeader({
  title,
  backTo,
  backDisabled = false,
  actions,
}: {
  title: ReactNode
  backTo?: string
  backDisabled?: boolean
  actions?: ReactNode
}) {
  const { t } = useTranslation("common")
  const back = useMobileBack(backTo ?? "/chats")
  const headerRef = useRef<HTMLElement>(null)
  const startRef = useRef<HTMLDivElement>(null)
  const endRef = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const header = headerRef.current
    const start = startRef.current
    const end = endRef.current
    if (!header || !start || !end) return

    /** 按两侧操作区的最大宽度预留标题空间。 */
    const updateSideWidth = () => {
      header.style.setProperty(
        "--mobile-header-side-width",
        `${Math.max(start.offsetWidth, end.offsetWidth)}px`,
      )
    }
    updateSideWidth()
    const observer = new ResizeObserver(updateSideWidth)
    observer.observe(start)
    observer.observe(end)
    return () => observer.disconnect()
  }, [])
  useEffect(() => {
    if (!backTo) return
    // 浮层处理后，详情页让系统返回与标题返回走同一条路径。
    const handleBack = (event: Event) => {
      if (event.defaultPrevented) return
      event.preventDefault()
      if (!backDisabled) back()
    }
    window.addEventListener("app:back", handleBack)
    return () => window.removeEventListener("app:back", handleBack)
  }, [back, backTo, backDisabled])
  return (
    <header
      ref={headerRef}
      className="grid h-12 shrink-0 grid-cols-[var(--mobile-header-side-width,0px)_minmax(0,1fr)_var(--mobile-header-side-width,0px)] items-center gap-2 border-b bg-sidebar px-4"
    >
      <div ref={startRef} className="flex w-max items-center">
        {backTo ? (
          <Button
            className="-ml-2"
            variant="ghost"
            size="icon-lg"
            aria-label={t("actions.back")}
            disabled={backDisabled}
            onClick={back}
          >
            <ArrowLeftIcon />
          </Button>
        ) : null}
      </div>
      <h1 className="min-w-0 max-w-full justify-self-center truncate text-center text-base font-semibold tracking-tight">
        {title}
      </h1>
      <div
        ref={endRef}
        className="flex w-max items-center justify-self-end gap-2"
      >
        {actions}
      </div>
    </header>
  )
}

/** 移动端资料分组：小标题与上下带分隔线的字段列表。 */
export function MobileProfileSection({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <section className="[section+&]:mt-6">
      <h3 className="pb-2 text-xs font-medium text-muted-foreground">{title}</h3>
      <dl className="divide-y border-y">{children}</dl>
    </section>
  )
}

/** 上下排列的资料字段行，需放在 dl 内。 */
export function MobileProfileField({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className="py-4">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 flex min-w-0 items-center text-sm">{children}</dd>
    </div>
  )
}

/** 在列表顶部显示带放大镜图标的单行搜索框，标签只供读屏使用。 */
export function MobileSearchBar({
  label,
  value,
  onChange,
}: {
  label: string
  value: string
  onChange: (value: string) => void
}) {
  return (
    <div className="shrink-0 border-b px-4 py-2">
      <div className="relative">
        <SearchIcon
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          type="search"
          enterKeyHint="search"
          aria-label={label}
          className="min-h-11 rounded-lg bg-muted/60 pl-9 md:text-base dark:bg-muted/50"
          value={value}
          onChange={(event) => onChange(event.target.value)}
        />
      </div>
    </div>
  )
}

/** 按页面和筛选条件保存滚动位置，数据就绪后恢复。 */
export function MobileScrollArea({
  storageKey,
  ready = true,
  children,
  className,
}: {
  storageKey: string
  ready?: boolean
  children: ReactNode
  className?: string
}) {
  const { scrollPositions } = useMobileNavigation()
  const element = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (ready && element.current) {
      element.current.scrollTop = scrollPositions.get(storageKey) ?? 0
    }
  }, [ready, scrollPositions, storageKey])
  return (
    <div
      key={storageKey}
      ref={element}
      className={cn(
        "min-h-0 flex-1 overflow-y-auto overscroll-contain",
        className,
      )}
      onScroll={(event) => {
        if (ready)
          scrollPositions.set(storageKey, event.currentTarget.scrollTop)
      }}
    >
      {children}
    </div>
  )
}

/** 显示空白、未开放或加载失败状态，并按需提供重试。 */
export function MobilePageState({
  title,
  description,
  onRetry,
}: {
  title: string
  description?: string
  onRetry?: () => void
}) {
  const { t } = useTranslation("common")
  return (
    <div
      className="flex min-h-64 flex-1 flex-col items-center justify-center px-6 py-12 text-center"
      role="status"
    >
      <p className="text-base font-medium">{title}</p>
      {description ? (
        <p className="mt-2 max-w-xs text-sm leading-6 text-muted-foreground">
          {description}
        </p>
      ) : null}
      {onRetry ? (
        <Button className="mt-4 min-h-11" variant="outline" onClick={onRetry}>
          {t("actions.retry")}
        </Button>
      ) : null}
    </div>
  )
}
