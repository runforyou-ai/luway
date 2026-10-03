/** 统一的页面数据读取 hook，封装 TanStack Query 并约束项目取数行为。 */
import { useCallback, useEffect, useMemo } from "react"
import {
  useQuery,
  useInfiniteQuery,
  useQueryClient,
  keepPreviousData,
  hashKey,
  type QueryKey,
} from "@tanstack/react-query"
import { useNavigate } from "react-router"

import type { PageInfo } from "@/api"
import { recoverSession } from "@/lib/session-navigation"

/** TanStack Query 对占位数据的约束：占位值不能是函数。 */
type PlaceholderValue<T> = T extends Function ? never : T

/**
 * 读取一份以 key 标识的页面数据。
 * key 相同的调用在多标签间共享缓存；load 收到的 signal 用于丢弃过期结果，绑定调用持续至完成。
 * 交互触发的一次性读取使用 useResourceReader。
 * 带会话状态的读取错误统一导航回对应入口。
 * 跨业务域的选项类数据用 staleTime: 0 让每次挂载都重新读取。
 * 状态字段以 getter 返回，组件只在实际读取的查询属性变化时重新渲染。
 * placeholder 在首次读取完成前提供占位数据，不写入缓存。
 */
export function useResource<T>(
  key: QueryKey,
  load: (signal: AbortSignal) => Promise<T>,
  options: {
    keepPreviousData?: boolean
    enabled?: boolean
    gcTime?: number
    staleTime?: number
    refetchInterval?: number | false | ((data: T | undefined) => number | false)
    refetchIntervalInBackground?: boolean
    refetchOnWindowFocus?: boolean
    placeholder?: () => T | undefined
  } = {},
) {
  const navigate = useNavigate()
  const refetchInterval = options.refetchInterval
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => load(signal),
    placeholderData: options.keepPreviousData
      ? keepPreviousData
      : options.placeholder && (() => options.placeholder?.() as PlaceholderValue<T> | undefined),
    enabled: options.enabled,
    staleTime: options.staleTime,
    gcTime: options.gcTime,
    refetchInterval: typeof refetchInterval === "function"
      ? (query) => refetchInterval(query.state.data)
      : refetchInterval,
    refetchIntervalInBackground: options.refetchIntervalInBackground,
    refetchOnWindowFocus: options.refetchOnWindowFocus,
  })

  const sessionError = query.error
  useEffect(() => {
    if (sessionError) {
      recoverSession(sessionError, navigate)
    }
  }, [sessionError, navigate])

  return {
    data: query.data,
    get dataUpdatedAt() {
      return query.dataUpdatedAt
    },
    get isPlaceholderData() {
      return query.isPlaceholderData
    },
    get loading() {
      return query.isPending && query.isFetching
    },
    get refreshing() {
      return query.isFetching && !query.isPending
    },
    // 读取失败后正在重试，页面据此把错误提示换回加载状态。
    get retrying() {
      return Boolean(query.error) && query.isFetching && !query.isPending
    },
    error: query.error,
    refresh: query.refetch,
  }
}

/** 返回按统一资源 key 执行交互触发读取的函数，共享缓存及会话错误恢复。 */
export function useResourceReader() {
  const navigate = useNavigate()
  const client = useQueryClient()
  return useCallback(
    async <R>(
      key: QueryKey,
      load: (signal: AbortSignal) => Promise<R>,
    ) => {
      try {
        return await client.fetchQuery({
          queryKey: key,
          queryFn: ({ signal }) => load(signal),
          staleTime: 0,
        })
      } catch (error) {
        recoverSession(error, navigate)
        throw error
      }
    },
    [client, navigate],
  )
}

type ResourceInvalidationOptions = {
  exact?: boolean
  refetchType?: "active" | "inactive" | "all" | "none"
}

/** 返回按 key 失效缓存的函数，供数据变更后刷新相关读取。 */
export function useResourceInvalidator() {
  const client = useQueryClient()
  return useCallback(
    (key: QueryKey, options: ResourceInvalidationOptions = {}) =>
      client.invalidateQueries({ queryKey: key, ...options }),
    [client],
  )
}

/** 返回按 key 移除缓存的函数，供离开所属页面上下文时丢弃不再复用的读取结果。 */
export function useResourceRemover() {
  const client = useQueryClient()
  return useCallback(
    (key: QueryKey) => client.removeQueries({ queryKey: key }),
    [client],
  )
}

/** 读取可双向追加的分页资源，并统一恢复失效会话。 */
export function useInfiniteResource<T>(
  key: QueryKey,
  load: (page: number, signal: AbortSignal) => Promise<T>,
  options: { getNextPage: (page: T) => number | undefined; getPreviousPage: (page: T) => number | undefined },
) {
  const navigate = useNavigate()
  const query = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam, signal }) => load(pageParam, signal),
    initialPageParam: 0,
    getNextPageParam: options.getNextPage,
    getPreviousPageParam: options.getPreviousPage,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    gcTime: 0,
  })
  useEffect(() => {
    if (query.error) recoverSession(query.error, navigate)
  }, [query.error, navigate])
  return query
}

/** 各分页列表在当前会话中已加载的页数，按查询 key 记录。 */
const pagedResourcePageCounts = new Map<string, number>()

/**
 * 滚动追加读取的列表下一页状态：ready 表示还有下一页且当前没有进行中的读取；
 * restoring 表示仍在展示上一查询的占位数据，或正在补齐上次离开时已加载的页，调用方据此推迟滚动恢复。
 */
export type PagedResourceMore = {
  ready: boolean
  restoring: boolean
  loading: boolean
  failed: boolean
  load: () => unknown
}

/**
 * 读取滚动追加的分页列表：从第一页起逐页读取，按条目 key 去重拼接，缓存保留已加载的页。
 * 缓存回收后重新进入时，先连续读取到本会话上次已加载的页数，再交给调用方恢复滚动位置。
 * 失效或 refresh 时按已加载页数重新读取；读取错误统一恢复会话。
 * select 与 itemKey 必须是只依赖入参的纯函数：拼接结果只在已加载的页变化时重新计算。
 */
export function usePagedResource<T, I>(
  key: QueryKey,
  load: (page: number, signal: AbortSignal) => Promise<T>,
  options: {
    // 纯函数，不读取组件状态；按筛选等条件变化的结果通过查询 key 区分。
    select: (data: T) => { items: readonly I[]; page: PageInfo }
    // 纯函数，不读取组件状态。
    itemKey: (item: I) => string
    keepPreviousData?: boolean
    enabled?: boolean
    staleTime?: number
    refetchInterval?: (items: readonly I[]) => number | false
    refetchOnWindowFocus?: boolean
  },
) {
  const navigate = useNavigate()
  const { select, itemKey, refetchInterval } = options
  const query = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam, signal }) => load(pageParam, signal),
    initialPageParam: 1,
    getNextPageParam: (last: T) => {
      const { page } = select(last)
      return page.number * page.size < page.total ? page.number + 1 : undefined
    },
    placeholderData: options.keepPreviousData ? keepPreviousData : undefined,
    enabled: options.enabled,
    staleTime: options.staleTime,
    refetchInterval: refetchInterval
      ? (query) => {
          const pages = query.state.data?.pages
          return pages ? refetchInterval(pages.flatMap((page) => select(page).items)) : false
        }
      : undefined,
    refetchOnWindowFocus: options.refetchOnWindowFocus,
  })

  const sessionError = query.error
  useEffect(() => {
    if (sessionError) recoverSession(sessionError, navigate)
  }, [sessionError, navigate])

  const pages = query.data?.pages
  const countKey = hashKey(key)
  const pageCount = query.isPlaceholderData ? 0 : (pages?.length ?? 0)
  const filling =
    pageCount > 0 &&
    pageCount < (pagedResourcePageCounts.get(countKey) ?? 1) &&
    query.hasNextPage
  const restoring = query.isPlaceholderData || filling
  const { isFetching, isFetchNextPageError, fetchNextPage } = query

  // 补齐上次已加载的页数，读取失败时保留目标页数等待重试；补齐后记录当前页数供下次进入使用。
  useEffect(() => {
    if (filling) {
      if (!isFetching && !isFetchNextPageError) void fetchNextPage()
    } else if (pageCount > 0) {
      pagedResourcePageCounts.set(countKey, pageCount)
    }
  }, [filling, isFetching, isFetchNextPageError, fetchNextPage, pageCount, countKey])

  // 滚动期间有数据增删时，后续页可能与已加载的页重复，按条目 key 保留首次出现的条目。
  const data = useMemo(() => {
    if (!pages) return undefined
    const seen = new Set<string>()
    return {
      items: pages.flatMap((page) =>
        select(page).items.filter((item) => {
          const id = itemKey(item)
          if (seen.has(id)) return false
          seen.add(id)
          return true
        }),
      ),
      total: select(pages[pages.length - 1]).page.total,
    }
  }, [pages])

  const { hasNextPage, isFetchingNextPage } = query
  const more: PagedResourceMore = useMemo(
    () => ({
      ready: hasNextPage && !isFetching && !isFetchNextPageError && !restoring,
      restoring,
      loading: isFetchingNextPage,
      failed: isFetchNextPageError,
      load: fetchNextPage,
    }),
    [hasNextPage, isFetching, isFetchNextPageError, restoring, isFetchingNextPage, fetchNextPage],
  )

  return {
    data,
    isPlaceholderData: query.isPlaceholderData,
    loading: query.isPending && query.isFetching,
    refreshing: query.isFetching && !query.isPending && !query.isFetchingNextPage,
    retrying: Boolean(query.error) && query.isFetching && !query.isPending,
    error: query.error,
    refresh: query.refetch,
    more,
  }
}
