/** 支持锚点定位和双向分页的分段阅读弹窗。 */
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react"
import { useTranslation } from "react-i18next"
import { isApiError, listKnowledgeDocumentSegments } from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useInfiniteResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { cn } from "@/lib/utils"

/** 从文档开头或检索命中位置打开分段，向两个方向滚动加载。 */
export function KnowledgeSegmentsDialog({ knowledgeBaseId, documentId, documentName, segmentId, segmentBatchId, onClose, triggerRef }: {
  knowledgeBaseId: string
  documentId: string
  documentName: string
  segmentId?: string
  segmentBatchId: string
  onClose: () => void
  triggerRef: RefObject<HTMLButtonElement | null>
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const viewport = useRef<HTMLDivElement>(null)
  const top = useRef<HTMLDivElement>(null)
  const bottom = useRef<HTMLDivElement>(null)
  const anchor = useRef<{ height: number } | null>(null)
  const [positioned, setPositioned] = useState(false)
  const resource = useInfiniteResource(
    resourceKeys.knowledgeDocumentSegments(knowledgeBaseId, documentId, segmentBatchId, segmentId),
    (page, signal) => listKnowledgeDocumentSegments(knowledgeBaseId, documentId, {
      page: page || (segmentId ? 0 : 1), pageSize: 20,
      anchorSegmentId: page === 0 ? segmentId ?? "" : "",
      segmentBatchId,
    }, signal),
    {
      getNextPage: ({ page }) => page.number * page.size < page.total ? page.number + 1 : undefined,
      getPreviousPage: ({ page }) => page.number > 1 ? page.number - 1 : undefined,
    },
  )
  const pages = resource.data?.pages

  // 初次定位命中段，并在页尾时补齐后续内容。
  useLayoutEffect(() => {
    const pane = viewport.current
    if (!pages || !pane) return
    if (!positioned) {
      const target = Array.from(pane.querySelectorAll<HTMLElement>("[data-segment-id]")).find((element) => element.dataset.segmentId === segmentId)
      if (target) {
        const desired = pane.scrollTop + target.getBoundingClientRect().top - pane.getBoundingClientRect().top - 24
        pane.scrollTop = desired
        // 命中段在页尾时补齐下一页，再完成定位。
        if (pane.scrollTop + 1 < desired && resource.hasNextPage && !resource.isFetchNextPageError) {
          if (!resource.isFetching) void resource.fetchNextPage()
          return
        }
      }
      setPositioned(true)
    }
  }, [pages, positioned, segmentId, resource.hasNextPage, resource.isFetchNextPageError, resource.isFetching, resource.fetchNextPage])

  // 分段页插入后按新增内容高度恢复阅读位置。
  useLayoutEffect(() => {
    const pane = viewport.current
    if (!pages || !pane || !anchor.current) return
    pane.scrollTop += pane.scrollHeight - anchor.current.height
    anchor.current = null
  }, [pages])

  // 阅读边界进入窗口时加载相邻页，加载失败时等待手动重试。
  useEffect(() => {
    if (!positioned || !viewport.current || resource.isFetching) return
    const observer = new IntersectionObserver((entries) => {
      const pane = viewport.current
      if (!pane) return
      if (entries.some((entry) => entry.target === top.current && entry.isIntersecting) && resource.hasPreviousPage && !resource.isFetchPreviousPageError) {
        anchor.current = { height: pane.scrollHeight }
        void resource.fetchPreviousPage().then((result) => { if (result.isError) anchor.current = null })
      } else if (entries.some((entry) => entry.target === bottom.current && entry.isIntersecting) && resource.hasNextPage && !resource.isFetchNextPageError) {
        void resource.fetchNextPage()
      }
    }, { root: viewport.current, rootMargin: "160px" })
    if (top.current) observer.observe(top.current)
    if (bottom.current) observer.observe(bottom.current)
    return () => observer.disconnect()
  }, [positioned, resource.isFetching, resource.hasPreviousPage, resource.hasNextPage, resource.isFetchPreviousPageError, resource.isFetchNextPageError, resource.fetchPreviousPage, resource.fetchNextPage])

  return <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
    <DialogContent className="flex h-[min(48rem,calc(100svh-2rem))] max-w-3xl flex-col gap-0 overflow-hidden p-0" aria-describedby={undefined}
      onOpenAutoFocus={(event) => { event.preventDefault(); viewport.current?.focus() }}
      onCloseAutoFocus={(event) => { event.preventDefault(); triggerRef.current?.focus({ preventScroll: true }) }}>
      <DialogHeader className="shrink-0 border-b px-6 py-5 pr-12">
        <DialogTitle>{documentName} · {t("documentDetail.viewSegments")}</DialogTitle>
        {pages && <p className="text-sm text-muted-foreground">{t("documentDetail.segments.total", { count: pages[0].page.total })}</p>}
      </DialogHeader>
      <div ref={viewport} tabIndex={0} className="min-h-0 flex-1 overflow-y-auto px-6 outline-none [overflow-anchor:none]" aria-label={t("documentDetail.viewSegments")}>
        {resource.isPending ? <LoadingIndicator className="min-h-48 justify-center">{t("common:status.loading")}</LoadingIndicator> : !pages ?
          <div className="py-12 text-center text-sm text-muted-foreground"><p>{isApiError(resource.error) ? apiErrorMessage(resource.error) : t("documentDetail.segments.error")}</p><Button className="mt-4" variant="outline" onClick={() => void resource.refetch()}>{t("common:actions.retry")}</Button></div> : <>
          <div ref={top} className="flex min-h-px items-center justify-center text-sm text-muted-foreground">
            {resource.isFetchingPreviousPage ? <LoadingIndicator className="py-3">{t("common:status.loading")}</LoadingIndicator> : resource.isFetchPreviousPageError ? <Button variant="link" onClick={() => {
              const pane = viewport.current
              if (pane) anchor.current = { height: pane.scrollHeight }
              void resource.fetchPreviousPage().then((result) => { if (result.isError) anchor.current = null })
            }}>{t("documentDetail.segments.retryPrevious")}</Button> : null}
          </div>
          {pages.flatMap((page) => page.segments).map((segment) => <article key={segment.id} data-segment-id={segment.id}
            className={cn("flex items-baseline gap-4 border-b py-1.5", segment.id === segmentId && "border-l-2 border-l-primary bg-primary/5 px-4")}>
            <div className="w-20 shrink-0 text-xs leading-7 text-muted-foreground">
              <span className="whitespace-nowrap">{t("documentDetail.segments.position", { position: segment.position })}</span>
              <span className="block">{t("documentDetail.segments.characters", { count: segment.characterCount })}</span>
              {segment.id === segmentId && <span className="block text-primary">{t("documentDetail.segments.matched")}</span>}
            </div>
            <div className="min-w-0 flex-1">
              {segment.context && <p className="whitespace-pre-wrap break-words pt-1.5 text-xs leading-5 text-muted-foreground select-text">{segment.context}</p>}
              <p className="whitespace-pre-wrap break-words text-sm leading-7 select-text">{segment.content}</p>
            </div>
          </article>)}
          <div ref={bottom} className="flex min-h-px items-center justify-center text-sm text-muted-foreground">
            {resource.isFetchingNextPage ? <LoadingIndicator className="py-3">{t("common:status.loading")}</LoadingIndicator> : resource.isFetchNextPageError ? <Button variant="link" onClick={() => void resource.fetchNextPage()}>{t("documentDetail.segments.retryNext")}</Button> : pages[0].page.total === 0 ? <p className="py-12">{t("documentDetail.segments.empty")}</p> : null}
          </div>
        </>}
      </div>
    </DialogContent>
  </Dialog>
}
