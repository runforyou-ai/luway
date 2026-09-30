/** 在稳定的预览区域内显示加载状态、原文件内容和不可预览提示。 */
import { lazy, Suspense, useState } from "react"
import { useTranslation } from "react-i18next"
import { readKnowledgeDocumentPreview, isApiError } from "@/api"
import { MessageMarkdown } from "@/components/message-markdown"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { parseDocumentPreview, type DocumentPreview } from "./document-preview-parser"

const PDFPreview = lazy(() =>
  import("./document-pdf-preview").then((module) => ({ default: module.DocumentPDFPreview })),
)

/** 读取受控原件并在浏览器解析，离开预览页即释放内容。 */
export function KnowledgeDocumentPreview({
  knowledgeBaseId,
  documentId,
  name,
}: {
  knowledgeBaseId: string
  documentId: string
  name: string
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const preview = useResource(
    resourceKeys.knowledgeDocumentFile(knowledgeBaseId, documentId),
    async (signal) => {
      // PPTX 返回格式暂不支持预览的状态。
      if (name.toLowerCase().endsWith(".pptx")) return { kind: "unsupported" } as const
      return parseDocumentPreview(name, await readKnowledgeDocumentPreview(knowledgeBaseId, documentId, signal))
    },
    { staleTime: Infinity, gcTime: 0, refetchOnWindowFocus: false },
  )
  const loading = (
    <LoadingIndicator className="h-full justify-center">{t("documentDetail.previewLoading")}</LoadingIndicator>
  )
  return (
    <section
      className="h-full min-h-0 overflow-hidden rounded-lg border bg-card"
      aria-label={t("documentDetail.preview")}
      aria-busy={preview.loading}
    >
      {preview.loading ? (
        loading
      ) : preview.error ? (
        <div className="flex h-full flex-col items-center justify-center gap-4 text-sm text-muted-foreground">
          <p>{isApiError(preview.error) ? apiErrorMessage(preview.error) : t("documentDetail.previewError")}</p>
          <Button variant="outline" onClick={() => void preview.refresh()}>
            {t("common:actions.retry")}
          </Button>
        </div>
      ) : preview.data ? (
        <Suspense fallback={loading}>
          <PreviewContent preview={preview.data} />
        </Suspense>
      ) : null}
    </section>
  )
}

/** 按文档类型展示原文、工作表或沙箱中的 HTML 内容。 */
function PreviewContent({ preview }: { preview: DocumentPreview }) {
  const { t, i18n } = useTranslation("knowledgeBase")
  const [sheet, setSheet] = useState(0)
  if (preview.kind === "unsupported")
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        {t("documentDetail.previewUnavailable")}
      </div>
    )
  if (preview.kind === "pdf") return <PDFPreview content={preview.content} />
  if (preview.kind === "markdown")
    return (
      <div className="h-full overflow-auto p-6 sm:p-8 select-text">
        <MessageMarkdown locale={i18n.language}>{preview.content}</MessageMarkdown>
      </div>
    )
  if (preview.kind === "text")
    return (
      <pre className="h-full overflow-auto whitespace-pre-wrap break-words p-6 font-sans text-sm leading-7 sm:p-8 select-text">
        {preview.content}
      </pre>
    )
  const content = preview.kind === "sheets" ? preview.sheets[sheet]?.content : preview.content
  return (
    <div className="flex h-full flex-col">
      {preview.kind === "sheets" && (
        <div
          className="flex shrink-0 gap-2 overflow-x-auto border-b p-3"
          role="tablist"
          aria-label={t("documentDetail.worksheets")}
        >
          {preview.sheets.map((item, index) => (
            <Button
              key={item.name}
              role="tab"
              aria-selected={sheet === index}
              variant={sheet === index ? "secondary" : "ghost"}
              size="sm"
              onClick={() => setSheet(index)}
            >
              {item.name}
            </Button>
          ))}
        </div>
      )}
      <iframe
        className="min-h-0 w-full flex-1 border-0 bg-white"
        sandbox=""
        referrerPolicy="no-referrer"
        title={preview.kind === "sheets" ? preview.sheets[sheet]?.name : t("documentDetail.preview")}
        srcDoc={content}
      />
    </div>
  )
}
