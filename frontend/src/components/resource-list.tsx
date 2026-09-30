/** 列表页的滚动容器、加载状态、表格容器和滚动续载。 */
import { useEffect, useRef, type ComponentProps, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useLocation } from "react-router"

import { LoadingIndicator } from "@/components/loading-indicator"
import { PageContent } from "@/components/page-content"
import {
  ResourceContent,
  resourceStatus,
  type ResourceState,
} from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { useListScrollRestore } from "@/hooks/use-list-scroll-restore"
import type { PagedResourceMore } from "@/hooks/use-resource"
import { cn } from "@/lib/utils"

/** 列表表格的容器，行内容与页面列左对齐；给出 more 时滚动接近列表末尾自动读取下一页。 */
export function ResourceListFrame({
  more,
  className,
  children,
  ...props
}: ComponentProps<"div"> & { more?: PagedResourceMore }) {
  return (
    <div
      data-slot="resource-list-frame"
      className={cn("relative flex flex-col", className)}
      {...props}
    >
      <div className="-mx-3">{children}</div>
      {more ? <ResourceListMore more={more} /> : null}
    </div>
  )
}

/** 放在列表之后的续载区，所在的定位容器同时包住列表：末尾一段进入可视范围时读取下一页，并显示读取中或失败重试。 */
export function ResourceListMore({ more }: { more: PagedResourceMore }) {
  return (
    <>
      <ResourceListSentinel more={more} />
      <ResourceListMoreStatus more={more} />
    </>
  )
}

/** 覆盖定位容器末尾一段高度的观察区域，进入可视范围时读取下一页。 */
function ResourceListSentinel({ more }: { more: PagedResourceMore }) {
  const sentinel = useRef<HTMLDivElement>(null)
  const { ready, load } = more

  // 每次恢复可读取状态时重新观察，首屏未填满时继续读取。
  useEffect(() => {
    const target = sentinel.current
    if (!ready || !target) return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void load()
    })
    observer.observe(target)
    return () => observer.disconnect()
  }, [ready, load])

  if (!ready) return null
  return (
    <div
      ref={sentinel}
      aria-hidden="true"
      className="pointer-events-none absolute inset-x-0 bottom-0 h-full max-h-96"
    />
  )
}

/** 列表末尾的续载状态：读取中显示加载提示，失败时提供重试。 */
function ResourceListMoreStatus({ more }: { more: PagedResourceMore }) {
  const { t } = useTranslation("common")
  if (more.loading)
    return (
      <LoadingIndicator className="justify-center py-4 text-xs">
        {t("status.loadingMore")}
      </LoadingIndicator>
    )
  if (more.failed)
    return (
      <div className="flex items-center justify-center gap-2 py-3 text-xs text-muted-foreground">
        {t("status.loadMoreError")}
        <Button type="button" variant="ghost" size="sm" onClick={() => void more.load()}>
          {t("actions.retry")}
        </Button>
      </div>
    )
  return null
}

/** 列表页主体：补齐上次已加载的页后恢复滚动位置，按加载状态渲染表格容器与滚动续载。 */
export function ResourceListLayout({
  resources,
  errorMessage,
  more,
  frameClassName,
  children,
}: {
  resources: ResourceState | readonly ResourceState[]
  errorMessage: string
  more?: PagedResourceMore
  frameClassName?: string
  children: ReactNode
}) {
  const location = useLocation()
  const scroll = useListScrollRestore(
    location.pathname + location.search,
    resourceStatus(resources).status === "ready" && !more?.restoring,
  )

  return (
    <PageContent ref={scroll.ref} onScroll={scroll.onScroll}>
      <ResourceContent resources={resources} errorMessage={errorMessage}>
        <ResourceListFrame more={more} className={frameClassName}>
          {children}
        </ResourceListFrame>
      </ResourceContent>
    </PageContent>
  )
}
