/** 保持列表滚动容器稳定，仅在读取失败时提供局部重试。 */
import { useEffect, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import type { InboxList } from "./use-inbox-list"
import type { useInboxListViewport } from "./use-inbox-list-viewport"

/** 共用列表加载状态，移动端保留原生触摸滚动。 */
export function InboxListPanel({ list, viewport, detailError = false, retryDetail, mobile = false, children }: {
  list: InboxList
  viewport: ReturnType<typeof useInboxListViewport>
  detailError?: boolean
  retryDetail?: () => void
  mobile?: boolean
  children: ReactNode
}) {
  const { t } = useTranslation(["inbox", "common"])
  const busy = list.operation !== null
  useEffect(() => {
    const container = viewport.element()
    if (!container) return
    // 内容不足一屏时自动补齐，恢复的深处窗口仍优先保持原邻域。
    if (!busy && !list.error && list.hasAfter && container.clientHeight > 0 && container.scrollHeight <= container.clientHeight + 120) void list.request("after")
  }, [busy, list.error, list.hasAfter, list.request, list.revision, viewport])
  // 页脚在可向前翻页、分页出错，以及空列表首次读取或翻页时占位；后台轮询与重读不占位。
  const previous = !list.conversations.length && list.hasBefore
  const pageError = list.error === "before" || list.error === "after"
  const loading = busy && (list.revision === 0 || list.operation === "before" || list.operation === "after")
  const showFooter = previous || pageError || (loading && !list.conversations.length)
  const content = (
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] pb-1.5">
      {children}
      {!list.conversations.length && (list.hasBefore || list.hasAfter) && list.revision > 0 ? (
        <p className="px-3 py-6 text-center text-sm text-muted-foreground">{t("listWindowEmpty")}</p>
      ) : null}
      {showFooter ? (
        <div className="flex h-10 items-center justify-center gap-2 text-xs text-muted-foreground" data-slot="inbox-list-footer" role="status">
          {previous ? (
            <Button variant="outline" size="sm" onClick={() => void list.request("before")}>{t("common:pagination.previous")}</Button>
          ) : null}
          {pageError ? (
            <Button variant="outline" size="sm" onClick={() => void list.retry()}>{t("common:actions.retry")}</Button>
          ) : loading ? <LoadingIndicator className="text-xs">{t("common:status.loading")}</LoadingIndicator> : null}
        </div>
      ) : null}
    </div>
  )
  return (
    <div ref={viewport.root} className="relative flex min-h-0 min-w-0 flex-1 flex-col" data-slot="inbox-list-panel">
      {(list.error && list.error !== "before" && list.error !== "after" && (!mobile || list.revision > 0)) || detailError ? (
        <div className="pointer-events-none absolute inset-x-2 bottom-2 z-10 flex items-center gap-2 rounded-md border bg-background px-3 py-2 shadow-sm" role="status">
          <span className="min-w-0 flex-1 text-xs text-muted-foreground">{t(list.error ? "inboxLoadError" : "conversationLoadError")}</span>
          <Button className="pointer-events-auto" variant="outline" size="sm" onClick={() => { void list.retry(); if (detailError) retryDetail?.() }}>{t("common:actions.retry")}</Button>
        </div>
      ) : null}
      {mobile ? (
        <div data-inbox-viewport className="min-h-0 flex-1 overflow-y-auto overscroll-contain [overflow-anchor:none]">{content}</div>
      ) : (
        <ScrollArea className="min-h-0 min-w-0 flex-1 [&>[data-slot=scroll-area-viewport]]:[overflow-anchor:none] [&>[data-slot=scroll-area-viewport]>div]:block">{content}</ScrollArea>
      )}
    </div>
  )
}
