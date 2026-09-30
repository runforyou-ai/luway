/** 使用本地 PDF.js 资源连续预览 PDF 页面及可选择正文。 */
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { getDocument, GlobalWorkerOptions, AnnotationMode } from "pdfjs-dist"
import { EventBus, PDFLinkService, PDFViewer } from "pdfjs-dist/web/pdf_viewer.mjs"
import workerURL from "pdfjs-dist/build/pdf.worker.min.mjs?url"
import "pdfjs-dist/web/pdf_viewer.css"
import { Button } from "@/components/ui/button"
import { LoadingIndicator } from "@/components/loading-indicator"

GlobalWorkerOptions.workerSrc = workerURL

/** 初始化连续 PDF 阅读器，在离开预览时释放工作线程和文档资源。 */
export function DocumentPDFPreview({ content }: { content: Uint8Array<ArrayBuffer> }) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const container = useRef<HTMLDivElement>(null)
  const viewerElement = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<"loading" | "ready" | "error">("loading")
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    setState("loading")
    if (!container.current || !viewerElement.current) return
    let active = true
    const eventBus = new EventBus()
    const linkService = new PDFLinkService({ eventBus, externalLinkTarget: 2, externalLinkRel: "noopener noreferrer" })
    const viewer = new PDFViewer({
      container: container.current,
      viewer: viewerElement.current,
      eventBus,
      linkService,
      annotationMode: AnnotationMode.ENABLE,
    })
    linkService.setViewer(viewer)
    eventBus.on("pagesinit", () => {
      viewer.currentScaleValue = "auto"
      if (active) setState("ready")
    })
    const base = new URL("pdfjs/", document.baseURI).href
    const task = getDocument({
      data: content.slice(),
      cMapUrl: `${base}cmaps/`,
      cMapPacked: true,
      standardFontDataUrl: `${base}standard_fonts/`,
      wasmUrl: `${base}wasm/`,
    })
    void task.promise
      .then((document) => {
        if (!active) return
        linkService.setDocument(document)
        viewer.setDocument(document)
      })
      .catch((error) => {
        if (active) {
          console.warn("PDF 文档预览失败", error)
          setState("error")
        }
      })
    const resize = new ResizeObserver(() => {
      if (viewer.pdfDocument) viewer.currentScaleValue = "auto"
    })
    resize.observe(container.current)
    return () => {
      active = false
      resize.disconnect()
      // PDF.js 运行时用 null 释放阅读器，当前声明未包含这个清理参数。
      // @ts-expect-error PDFViewer.setDocument 支持 null。
      viewer.setDocument(null)
      linkService.setDocument(null)
      void task.destroy()
    }
  }, [content, attempt])
  return (
    <div className="relative h-full min-h-0 bg-muted">
      <div ref={container} className="absolute inset-0 overflow-auto" tabIndex={0}>
        <div ref={viewerElement} className="pdfViewer" />
      </div>
      {state !== "ready" && (
        <div className="absolute inset-0 flex items-center justify-center bg-background">
          {state === "loading" ? (
            <LoadingIndicator>{t("documentDetail.previewLoading")}</LoadingIndicator>
          ) : (
            <div className="space-y-4 text-center">
              <p className="text-sm text-muted-foreground">{t("documentDetail.previewError")}</p>
              <Button variant="outline" onClick={() => setAttempt((value) => value + 1)}>
                {t("common:actions.retry")}
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
