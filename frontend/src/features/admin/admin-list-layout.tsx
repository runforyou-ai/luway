/** 平台后台列表页主体：可滚动的列表区加底部页码分页，离开再返回时恢复滚动位置。 */
import type { ReactNode } from "react"
import { useLocation } from "react-router"

import type { PageInfo } from "@/api"
import { AdminPagination } from "@/features/admin/admin-pagination"
import { ResourceContent, resourceStatus, type ResourceState } from "@/components/resource-content"
import { useListScrollRestore } from "@/hooks/use-list-scroll-restore"
import { cn } from "@/lib/utils"

/** 分页栏所需的数据与回调；page 尚未读取时不显示分页栏。 */
export type AdminListPagination = {
  page: PageInfo | undefined
  onPageChange: (page: number) => void
  onPageSizeChange?: (size: number) => void
  /** 正在展示上一页内容、等待新页到达。 */
  busy?: boolean
}

/** 按资源读取状态渲染列表，pagination 缺省时为不分页的列表。 */
export function AdminListLayout({
  resources,
  errorMessage,
  pagination,
  scrollKey,
  children,
}: {
  resources: ResourceState | readonly ResourceState[]
  errorMessage: string
  pagination?: AdminListPagination
  /** 记录滚动位置的键，默认为当前路径与查询参数；翻页会改变查询参数从而回到顶部。 */
  scrollKey?: string
  children: ReactNode
}) {
  const location = useLocation()
  const scroll = useListScrollRestore(
    scrollKey ?? location.pathname + location.search,
    resourceStatus(resources).status === "ready",
  )

  return (
    <div data-slot="admin-list-layout" className="flex min-h-0 flex-1 flex-col">
      <div
        ref={scroll.ref}
        data-slot="admin-list-body"
        className={cn("min-h-0 flex-1 overflow-auto px-6 py-3 transition-opacity", pagination?.busy && "opacity-60")}
        onScroll={scroll.onScroll}
      >
        <ResourceContent resources={resources} errorMessage={errorMessage}>
          {children}
        </ResourceContent>
      </div>
      {pagination?.page ? (
        <AdminPagination page={pagination.page} onPageChange={pagination.onPageChange} onPageSizeChange={pagination.onPageSizeChange} />
      ) : null}
    </div>
  )
}

/** 数据表的外框：圆角描边的卡片底，列多时可横向滚动。 */
export function AdminTableFrame({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("overflow-x-auto rounded-md border border-border bg-card", className)}>{children}</div>
}
