/** 平台后台列表的页码分页：总数、上一页、页码、下一页与可选的每页条数。 */
import { ChevronLeftIcon, ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import type { PageInfo } from "@/api"
import { Button } from "@/components/ui/button"
import { NativeSelect } from "@/components/ui/native-select"
import { adminPageSizes } from "@/hooks/use-admin-page-query"
import { cn } from "@/lib/utils"

/** 页码按钮中的一项：页码或省略号。 */
type PageItem = number | "start-gap" | "end-gap"

/** 计算要显示的页码：首尾页与当前页前后各一页，中间不连续处用省略号。 */
function pageItems(current: number, last: number): PageItem[] {
  if (last <= 7) return Array.from({ length: last }, (_, index) => index + 1)
  const items: PageItem[] = [1]
  const from = Math.max(2, current - 1)
  const to = Math.min(last - 1, current + 1)
  if (from > 2) items.push("start-gap")
  for (let number = from; number <= to; number++) items.push(number)
  if (to < last - 1) items.push("end-gap")
  items.push(last)
  return items
}

/** 渲染页码分页；没有数据时不显示。onPageSizeChange 缺省时不提供每页条数选择。 */
export function AdminPagination({
  page,
  onPageChange,
  onPageSizeChange,
  className,
}: {
  page: PageInfo
  onPageChange: (page: number) => void
  onPageSizeChange?: (size: number) => void
  className?: string
}) {
  const { t } = useTranslation("common")
  if (page.total === 0) return null
  const last = Math.max(1, Math.ceil(page.total / page.size))
  const current = Math.min(page.number, last)
  // 当前每页条数不在可选范围时也要出现在下拉中，避免显示与实际不一致。
  const sizes = adminPageSizes.some((size) => size === page.size) ? [...adminPageSizes] : [...adminPageSizes, page.size].sort((a, b) => a - b)

  return (
    <div
      data-slot="admin-pagination"
      className={cn("flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border bg-card px-6 py-2 select-none", className)}
    >
      <span className="text-xs text-muted-foreground tabular-nums">{t("pagination.total", { count: page.total })}</span>
      <div className="flex flex-wrap items-center gap-3">
        {onPageSizeChange ? (
          <NativeSelect
            aria-label={t("pagination.pageSize")}
            className="h-7 w-28 text-xs md:text-xs"
            value={page.size}
            onChange={(event) => onPageSizeChange(Number(event.target.value))}
          >
            {sizes.map((size) => (
              <option key={size} value={size}>
                {t("pagination.perPage", { count: size })}
              </option>
            ))}
          </NativeSelect>
        ) : null}
        <nav aria-label={t("pagination.navLabel")} className="flex items-center gap-1">
          <Button
            variant="outline"
            size="icon-sm"
            aria-label={t("pagination.previous")}
            title={t("pagination.previous")}
            disabled={current <= 1}
            onClick={() => onPageChange(current - 1)}
          >
            <ChevronLeftIcon />
          </Button>
          {pageItems(current, last).map((item) =>
            typeof item === "number" ? (
              <Button
                key={item}
                variant={item === current ? "default" : "ghost"}
                size="icon-sm"
                className="text-xs tabular-nums"
                aria-label={t("pagination.goToPage", { page: item })}
                aria-current={item === current ? "page" : undefined}
                onClick={() => (item === current ? undefined : onPageChange(item))}
              >
                {item}
              </Button>
            ) : (
              <span key={item} aria-hidden="true" className="w-7 text-center text-xs text-muted-foreground">
                …
              </span>
            ),
          )}
          <Button
            variant="outline"
            size="icon-sm"
            aria-label={t("pagination.next")}
            title={t("pagination.next")}
            disabled={current >= last}
            onClick={() => onPageChange(current + 1)}
          >
            <ChevronRightIcon />
          </Button>
        </nav>
      </div>
    </div>
  )
}
