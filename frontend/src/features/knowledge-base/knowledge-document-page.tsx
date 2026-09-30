/** 本地知识文档预览页面，保留列表返回位置。 */
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useLocation, useParams } from "react-router"
import {
  getKnowledgeDocument,
  getKnowledgeDocumentContent,
  KnowledgeDocumentSourceKind,
} from "@/api"
import { MessageMarkdown } from "@/components/message-markdown"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { KnowledgeSegmentsDialog } from "./knowledge-segments-dialog"
import { KnowledgeDocumentPreview } from "./knowledge-document-preview"

/** 按内容来源显示原件预览或正文，并提供固定批次的分段阅读入口。 */
export function KnowledgeDocumentPage() {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const { knowledgeBaseId = "", documentId = "" } = useParams()
  const location = useLocation()
  // 保存弹窗打开时的分段批次。
  const [segmentBatchId, setSegmentBatchId] = useState("")
  const trigger = useRef<HTMLButtonElement>(null)
  const document = useResource(
    resourceKeys.knowledgeDocument(knowledgeBaseId, documentId),
    (signal) => getKnowledgeDocument(knowledgeBaseId, documentId, signal),
    { staleTime: 0, refetchInterval: (data) => data?.status === "queued" || data?.status === "running" ? 2000 : false },
  )
  const uploaded =
    document.data?.sourceKind ===
    KnowledgeDocumentSourceKind.KnowledgeDocumentSourceFile
  const processing =
    document.data?.status === "queued" || document.data?.status === "running"
  const content = useResource(
    resourceKeys.knowledgeDocumentContent(knowledgeBaseId, documentId),
    (signal) => getKnowledgeDocumentContent(knowledgeBaseId, documentId, signal),
    {
      enabled: Boolean(document.data) && !uploaded,
      staleTime: 0,
      refetchInterval: () => (processing ? 2000 : false),
    },
  )
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={document.data?.name ?? t("documents.title")}
        description={t("documentDetail.description")}
        backTo={`/knowledge-bases/${knowledgeBaseId}/documents${location.search}`}
      >
        <Button ref={trigger} variant="outline" size="sm" disabled={!document.data?.segmentBatchId} onClick={() => setSegmentBatchId(document.data?.segmentBatchId ?? "")}>
          {t("documentDetail.viewSegments")}
        </Button>
      </PageHeader>
      {document.data?.sourceUrl ? (
        <div className="shrink-0 px-4 pt-4 text-sm text-muted-foreground sm:px-6 sm:pt-6">
          {t("documents.sourceOrigin")}
          <a
            href={document.data.sourceUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="underline underline-offset-4"
          >
            {document.data.sourceUrl}
          </a>
        </div>
      ) : null}
      <PageContent className="overflow-hidden">
        <ResourceContent resources={document} errorMessage={t("documents.loadError")}>
          {uploaded ? (
            <KnowledgeDocumentPreview
              key={documentId}
              knowledgeBaseId={knowledgeBaseId}
              documentId={documentId}
              name={document.data?.name ?? ""}
            />
          ) : (
            <ResourceContent resources={content} errorMessage={t("documents.loadError")}>
              <div className="h-full overflow-auto rounded-lg border bg-card px-6 py-5">
                {content.data?.content ? (
                  <MessageMarkdown>{content.data.content}</MessageMarkdown>
                ) : (
                  // 抓取尚未完成或已失败时说明当前状态，不呈现空白正文。
                  <p className="text-sm text-muted-foreground">
                    {processing
                      ? t("documents.contentPending")
                      : (document.data?.failureMessage ?? "") ||
                        t("documents.contentEmpty")}
                  </p>
                )}
              </div>
            </ResourceContent>
          )}
        </ResourceContent>
      </PageContent>
      {segmentBatchId && document.data && <KnowledgeSegmentsDialog
        knowledgeBaseId={knowledgeBaseId} documentId={documentId} documentName={document.data.name}
        segmentBatchId={segmentBatchId} triggerRef={trigger} onClose={() => setSegmentBatchId("")}
      />}
    </div>
  )
}
