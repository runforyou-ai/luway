/** 移动端列表的逐页加载、返回恢复和加载状态提示。 */
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react"
import { useTranslation } from "react-i18next"

import type { PageInfo } from "@/api"
import { useMobileNavigation } from "@/apps/mobile/shared/mobile-navigation"
import { MobilePageState, MobileScrollArea } from "@/apps/mobile/shared/mobile-page"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { useResource } from "@/hooks/use-resource"

/** 一页数据的查询 key 和读取方法，缓存的始终是接口原始响应。 */
type MobilePagedSource<D> = {
  key: unknown[]
  load: (signal?: AbortSignal) => Promise<D>
}

/** 列表各状态使用的文案。 */
type MobilePagedLabels = {
  loadError: string
  empty: string
  allLoaded: string
}

/** 保留各查询条件已加载的页数，恢复全部原有页面后再恢复滚动位置。 */
export function MobilePagedList<T, D>({
  storageKey,
  searching = false,
  labels,
  source,
  select,
  children,
}: {
  storageKey: string
  searching?: boolean
  labels: MobilePagedLabels
  source: (page: number) => MobilePagedSource<D>
  select: (data: D) => { items: T[]; page: PageInfo }
  children: (items: T[]) => ReactNode
}) {
  const { listPageCounts } = useMobileNavigation()
  const [pageCount, setPageCount] = useState(
    () => listPageCounts.get(storageKey) ?? 1,
  )
  const initialPageCount = useRef(pageCount)
  const loadedPages = useRef(new Set<number>())
  const [ready, setReady] = useState(false)

  useLayoutEffect(() => {
    listPageCounts.set(storageKey, pageCount)
  }, [listPageCounts, storageKey, pageCount])

  /** 初始页面全部就绪后完成定位并开始记录位置。 */
  const onReady = useCallback((page: number) => {
    loadedPages.current.add(page)
    if (loadedPages.current.size >= initialPageCount.current) setReady(true)
  }, [])

  /** 一次只追加当前尾页的下一页，忽略旧观察回调。 */
  const loadMore = useCallback((page: number) => {
    setPageCount((current) => (current === page ? current + 1 : current))
  }, [])

  return (
    <MobileScrollArea storageKey={storageKey} ready={ready}>
      {Array.from({ length: pageCount }, (_, index) => (
        <MobilePagedResults
          key={index + 1}
          page={index + 1}
          last={index + 1 === pageCount}
          autoLoad={ready && !searching}
          labels={labels}
          source={source}
          select={select}
          onReady={onReady}
          onLoadMore={loadMore}
        >
          {children}
        </MobilePagedResults>
      ))}
    </MobileScrollArea>
  )
}

/** 按统一资源 key 缓存一页数据，尾部进入滚动视口时加载下一页。 */
function MobilePagedResults<T, D>({
  page,
  last,
  autoLoad,
  labels,
  source,
  select,
  onReady,
  onLoadMore,
  children,
}: {
  page: number
  last: boolean
  autoLoad: boolean
  labels: MobilePagedLabels
  source: (page: number) => MobilePagedSource<D>
  select: (data: D) => { items: T[]; page: PageInfo }
  onReady: (page: number) => void
  onLoadMore: (page: number) => void
  children: (items: T[]) => ReactNode
}) {
  const { t } = useTranslation(["mobile", "common"])
  const sentinel = useRef<HTMLDivElement>(null)
  const { key, load } = source(page)
  const { data, loading, refreshing, error, refresh } = useResource(key, load)
  const view = data === undefined ? null : select(data)
  const items = view?.items ?? []
  const hasMore = Boolean(
    view && items.length > 0 && view.page.number * view.page.size < view.page.total,
  )

  useLayoutEffect(() => {
    if (view) onReady(page)
  }, [view, onReady, page])
  useEffect(() => {
    const element = sentinel.current
    if (!element || !last || !autoLoad || !hasMore || error || refreshing)
      return
    // 各页使用 Fragment，哨兵始终是列表滚动容器的直接子节点。
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) onLoadMore(page)
      },
      { root: element.parentElement, rootMargin: "0px 0px 160px 0px" },
    )
    observer.observe(element)
    return () => observer.disconnect()
  }, [last, autoLoad, hasMore, error, refreshing, page, onLoadMore])

  return (
    <>
      {loading && !view ? (
        <LoadingIndicator
          className={
            page === 1 ? "min-h-64 justify-center" : "h-14 justify-center"
          }
        >
          {t(page === 1 ? "common:status.loading" : "common:status.loadingMore")}
        </LoadingIndicator>
      ) : null}
      {error && !view && page === 1 ? (
        <MobilePageState title={labels.loadError} onRetry={() => void refresh()} />
      ) : error ? (
        <div className="flex h-14 items-center justify-center">
          <Button
            variant="outline"
            className="min-h-11"
            disabled={loading || refreshing}
            onClick={() => void refresh()}
          >
            {t("common:status.loadMoreError")}
            {" · "}
            {t("common:actions.retry")}
          </Button>
        </div>
      ) : null}
      {view && page === 1 && items.length === 0 && !error ? (
        <MobilePageState title={labels.empty} />
      ) : null}
      {view && items.length > 0 ? children(items) : null}
      {last && view && !error && (hasMore || page > 1) ? (
        <div
          ref={sentinel}
          className="flex h-14 items-center justify-center text-sm text-muted-foreground"
          role="status"
        >
          {!hasMore ? labels.allLoaded : null}
        </div>
      ) : null}
    </>
  )
}
