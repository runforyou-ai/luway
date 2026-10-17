/** 平台后台页码分页列表的读取：当前页码与每页条数同步到地址参数。 */
import { useCallback, useEffect } from "react"
import type { QueryKey } from "@tanstack/react-query"
import { useSearchParams } from "react-router"

import type { PageInfo } from "@/api"
import { useResource } from "@/hooks/use-resource"

/** 后台列表可选的每页条数。 */
export const adminPageSizes = [20, 50, 100] as const

/** 后台列表缺省的每页条数。 */
export const adminDefaultPageSize = 50

/** 读取一页数据时的页码与每页条数。 */
export type AdminPageRequest = {
  page: number
  pageSize: number
}

/** 解析地址中的页码，缺省或无效时为第 1 页。 */
function parsePage(value: string | null) {
  const page = Number(value)
  return Number.isInteger(page) && page >= 1 ? page : 1
}

/** 解析地址中的每页条数，不在可选范围内时取缺省值。 */
function parsePageSize(value: string | null) {
  const size = Number(value)
  return adminPageSizes.find((option) => option === size) ?? adminDefaultPageSize
}

/**
 * 按地址中的 page 与 size 参数读取一页数据；key 不含页码，筛选条件变化通过 key 区分。
 * 切换页码时保留上一页内容直到新数据到达；页码超出范围时回到最后一页。
 * 会话错误与 useResource 一致统一恢复。筛选条件变化时调用方负责清除 page 参数。
 */
export function useAdminPageQuery<T extends { page: PageInfo }>(
  key: QueryKey,
  load: (request: AdminPageRequest, signal: AbortSignal) => Promise<T>,
  options: {
    enabled?: boolean
    staleTime?: number
    refetchInterval?: number | false | ((data: T | undefined) => number | false)
  } = {},
) {
  const [searchParams, setSearchParams] = useSearchParams()
  const page = parsePage(searchParams.get("page"))
  const pageSize = parsePageSize(searchParams.get("size"))
  const resource = useResource(
    [...key, "page", page, pageSize],
    (signal) => load({ page, pageSize }, signal),
    { ...options, keepPreviousData: true },
  )

  /** 跳转到指定页，第 1 页不写入地址；越界修正使用替换以免留下无效历史。 */
  const setPage = useCallback(
    (next: number, replace = false) => {
      setSearchParams(
        (current) => {
          const parameters = new URLSearchParams(current)
          if (next <= 1) parameters.delete("page")
          else parameters.set("page", String(next))
          return parameters
        },
        { replace },
      )
    },
    [setSearchParams],
  )

  /** 修改每页条数并回到第 1 页。 */
  const setPageSize = useCallback(
    (next: number) => {
      setSearchParams((current) => {
        const parameters = new URLSearchParams(current)
        parameters.delete("page")
        if (next === adminDefaultPageSize) parameters.delete("size")
        else parameters.set("size", String(next))
        return parameters
      })
    },
    [setSearchParams],
  )

  const info = resource.data?.page
  const placeholder = resource.isPlaceholderData
  useEffect(() => {
    if (!info || placeholder) return
    const last = Math.max(1, Math.ceil(info.total / info.size))
    if (page > last) setPage(last, true)
  }, [info, placeholder, page, setPage])

  return {
    data: resource.data,
    page,
    pageSize,
    setPage,
    setPageSize,
    error: resource.error,
    refresh: resource.refresh,
    isLoading: resource.loading,
    refreshing: resource.refreshing,
    retrying: resource.retrying,
    isPlaceholderData: placeholder,
  }
}
