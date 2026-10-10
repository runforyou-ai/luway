/** 文档列表按单列行布局展示名称、索引状态和来源信息，文档操作通过行操作菜单完成。 */
import { useMutation, useMutationState } from "@tanstack/react-query"
import { refetchKnowledgeDocument, retryKnowledgeDocument, KnowledgeDocumentSourceKind } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator, type PagedResourceMore } from "@/hooks/use-resource"
import { useNavigate } from "react-router"
import {
  FileBracesIcon,
  FileCodeIcon,
  FileIcon,
  FileSpreadsheetIcon,
  FileTextIcon,
  FileTypeIcon,
  GlobeIcon,
  PencilLineIcon,
  PresentationIcon,
  type LucideIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"
import type { KnowledgeDocument } from "@/api"
import { ResourceListFrame } from "@/components/resource-list"
import { ResourceTable } from "@/components/resource-table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useDateTime } from "@/hooks/use-date-time"
import { formatFileSize } from "@/lib/file-size"
import { cn } from "@/lib/utils"
import { useReturnLink } from "@/hooks/use-return-to"
import { KnowledgeIndexStatus } from "./knowledge-index-status"

/** 各格式文档在行首显示的彩色图标，未列出的格式使用灰色通用文件图标。 */
const formatIcons: Record<string, { icon: LucideIcon; className: string }> = {
  ".pdf": { icon: FileTextIcon, className: "bg-file-pdf/12 text-file-pdf" },
  ".docx": { icon: FileTypeIcon, className: "bg-file-word/12 text-file-word" },
  ".pptx": { icon: PresentationIcon, className: "bg-file-slide/12 text-file-slide" },
  ".xlsx": { icon: FileSpreadsheetIcon, className: "bg-file-sheet/12 text-file-sheet" },
  ".csv": { icon: FileSpreadsheetIcon, className: "bg-file-sheet/12 text-file-sheet" },
  ".json": { icon: FileBracesIcon, className: "bg-file-code/12 text-file-code" },
  ".html": { icon: FileCodeIcon, className: "bg-file-code/12 text-file-code" },
  ".htm": { icon: FileCodeIcon, className: "bg-file-code/12 text-file-code" },
}
const defaultFormatIcon = { icon: FileIcon, className: "bg-file-text/12 text-file-text" }

/** 非上传来源在格式图标右下角叠加的角标；上传文件是默认来源，不加角标。 */
const sourceBadgeIcons: Partial<Record<KnowledgeDocumentSourceKind, LucideIcon>> = {
  [KnowledgeDocumentSourceKind.KnowledgeDocumentSourceWeb]: GlobeIcon,
  [KnowledgeDocumentSourceKind.KnowledgeDocumentSourceText]: PencilLineIcon,
}

/** 扩展名对应的格式名称，未列出的格式显示大写扩展名。 */
const formatNames: Record<string, string> = {
  ".md": "Markdown",
  ".markdown": "Markdown",
  ".htm": "HTML",
}

/** 返回悬停在格式图标上时显示的格式名称。 */
function formatName(format: string) {
  return formatNames[format] ?? format.slice(1).toUpperCase()
}

/** 显示文档列表，右键行或点行尾「⋯」打开文档操作菜单。 */
export function KnowledgeDocumentTable({
  knowledgeBaseId,
  documents,
  more,
  listPath,
  filtered,
  refreshing,
  onDelete,
}: {
  knowledgeBaseId: string
  documents: readonly KnowledgeDocument[]
  more: PagedResourceMore
  listPath: string
  filtered: boolean
  refreshing: boolean
  onDelete: (document: KnowledgeDocument) => void
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const { formatDateTime } = useDateTime()
  const invalidate = useResourceInvalidator()
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const returnLink = useReturnLink()

  const retryKey = ["knowledgeDocumentRetry", knowledgeBaseId]
  // 提交重试或重新抓取，并在结束后刷新列表和详情中的文档状态。
  const retry = useMutation({
    mutationKey: retryKey,
    mutationFn: async ({ documentId, refetch }: { documentId: string; refetch: boolean }) => {
      if (refetch) await refetchKnowledgeDocument(knowledgeBaseId, documentId, { sourceUrl: "" })
      else await retryKnowledgeDocument(knowledgeBaseId, documentId)
    },
    onError: (error, { refetch }) =>
      reportError(error, { fallback: t(refetch ? "documents.refetchFailed" : "documents.retryFailed") }),
    onSettled: (_data, _error, { documentId }) =>
      Promise.all([
        invalidate(resourceKeys.knowledgeDocuments(knowledgeBaseId)),
        invalidate(resourceKeys.knowledgeDocument(knowledgeBaseId, documentId)),
        invalidate(resourceKeys.knowledgeDocumentContent(knowledgeBaseId, documentId)),
      ]),
  })
  const retryingIds = new Set(
    useMutationState({
      filters: { mutationKey: retryKey, status: "pending" },
      select: (mutation) => (mutation.state.variables as { documentId: string }).documentId,
    }),
  )

  return (
    <ResourceListFrame aria-busy={refreshing} more={more}>
      <ResourceTable
        columns={[
          {
            key: "name",
            header: t("documents.columns.name"),
            cellClassName: "min-w-0",
            cell: (document) => {
              const { icon: FormatIcon, className: formatClassName } =
                formatIcons[document.format] ?? defaultFormatIcon
              const SourceBadgeIcon = sourceBadgeIcons[document.sourceKind]
              const label = `${formatName(document.format)} · ${t(`documents.sources.${document.sourceKind}`)}`
              return (
                <div className="flex min-w-0 items-center gap-3">
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span
                        role="img"
                        aria-label={label}
                        className={cn(
                          "relative flex size-9 shrink-0 items-center justify-center rounded-full",
                          formatClassName,
                        )}
                      >
                        <FormatIcon className="size-4.5" aria-hidden="true" />
                        {SourceBadgeIcon ? (
                          <span className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background text-muted-foreground ring-1 ring-border">
                            <SourceBadgeIcon className="size-2.5" aria-hidden="true" />
                          </span>
                        ) : null}
                      </span>
                    </TooltipTrigger>
                    <TooltipContent side="top">{label}</TooltipContent>
                  </Tooltip>
                  <span className="grid min-w-0 justify-items-start gap-1 leading-tight">
                    <span className="max-w-full truncate font-medium" title={document.name}>
                      {document.name}
                    </span>
                    <span className="flex items-center gap-2">
                      <KnowledgeIndexStatus status={document.status} failureMessage={document.failureMessage} />
                      <span className="text-xs text-muted-foreground tabular-nums">
                        {formatFileSize(document.byteSize)}
                      </span>
                    </span>
                  </span>
                </div>
              )
            },
          },
          {
            key: "details",
            header: t("documents.columns.updatedAt"),
            cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
            cell: (document) =>
              t("documents.columns.updatedAtTime", {
                time: formatDateTime(document.updatedAt),
              }),
          },
        ]}
        rows={documents}
        rowKey={(document) => document.id}
        empty={t(filtered ? "documents.filteredEmpty" : "documents.empty")}
        // 在线编写的文档直接进入编辑，其余打开详情查看。
        onRowActivate={(document) =>
          navigate(
            document.sourceKind === KnowledgeDocumentSourceKind.KnowledgeDocumentSourceText
              ? returnLink(`${listPath}/${document.id}/edit`)
              : returnLink(`${listPath}/${document.id}`),
          )
        }
        // 网页来源的文档额外提供重新抓取。
        rowActions={(document) => [
          {
            key: "reprocess",
            label: t("documents.reprocess"),
            disabled: retryingIds.has(document.id),
            onSelect: () => retry.mutate({ documentId: document.id, refetch: false }),
          },
          ...(document.sourceKind === KnowledgeDocumentSourceKind.KnowledgeDocumentSourceWeb
            ? [
                {
                  key: "refetch",
                  label: t("documents.refetch"),
                  disabled: retryingIds.has(document.id),
                  onSelect: () => retry.mutate({ documentId: document.id, refetch: true }),
                },
              ]
            : []),
          {
            key: "delete",
            label: t("common:actions.delete"),
            destructive: true,
            separatorBefore: true,
            onSelect: () => onDelete(document),
          },
        ]}
      />
    </ResourceListFrame>
  )
}
