/** 展示问答列表和索引状态，滚动到末尾继续加载，问答操作通过行操作菜单完成。 */
import { useMutation, useMutationState } from "@tanstack/react-query"
import { CircleHelpIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { retryKnowledgeQAEntry, type KnowledgeQASummary } from "@/api"
import { ResourceListFrame } from "@/components/resource-list"
import { ResourceTable } from "@/components/resource-table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { KnowledgeIndexStatus } from "@/features/knowledge-base/knowledge-index-status"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator, type PagedResourceMore } from "@/hooks/use-resource"
import { useReturnLink } from "@/hooks/use-return-to"

/** 按单列行布局展示标准问题、索引状态、相似问题数、答案摘要和创建时间。 */
export function KnowledgeQATable({
  knowledgeBaseId,
  entries,
  more,
  loading,
  listPath,
  filtered,
  onDelete,
}: {
  knowledgeBaseId: string
  entries: readonly KnowledgeQASummary[]
  more: PagedResourceMore
  loading: boolean
  listPath: string
  filtered: boolean
  onDelete: (entry: KnowledgeQASummary) => void
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const returnLink = useReturnLink()
  const invalidate = useResourceInvalidator()
  const { formatDateTime } = useDateTime()
  const retryKey = ["knowledgeQARetry", knowledgeBaseId]
  // 按当前配置重新处理问答，并在结束后刷新列表中的状态。
  const retry = useMutation({
    mutationKey: retryKey,
    mutationFn: (entryId: string) => retryKnowledgeQAEntry(knowledgeBaseId, entryId),
    onError: (error) => reportError(error, { fallback: t("qa.retryFailed") }),
    onSettled: () => invalidate(resourceKeys.knowledgeQAEntries(knowledgeBaseId)),
  })
  const retryingIds = new Set(
    useMutationState({
      filters: { mutationKey: retryKey, status: "pending" },
      select: (mutation) => mutation.state.variables as string,
    }),
  )

  return (
    <ResourceListFrame aria-busy={loading} more={more}>
      <ResourceTable
        columns={[
          {
            key: "question",
            header: t("qa.question"),
            // max-w-0 让自动布局的表格按比例分配宽度，长文本在列内截断。
            cellClassName: "w-1/2 max-w-0",
            cell: (entry) => (
              <div className="flex min-w-0 items-center gap-3">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary/12 text-primary">
                  <CircleHelpIcon className="size-4.5" aria-hidden="true" />
                </span>
                <span className="grid min-w-0 justify-items-start gap-1 leading-tight">
                  <span className="max-w-full truncate font-medium" title={entry.question}>
                    {entry.question}
                  </span>
                  <span className="flex items-center gap-2">
                    <KnowledgeIndexStatus status={entry.status} failureMessage={entry.failureMessage} />
                    {entry.similarQuestions.length > 0 ? (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span
                            tabIndex={0}
                            className="cursor-help text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          >
                            {t("qa.similarCount", { count: entry.similarQuestions.length })}
                          </span>
                        </TooltipTrigger>
                        <TooltipContent side="bottom" sideOffset={4} className="max-w-sm">
                          <ul className="grid max-h-64 gap-1 overflow-y-auto text-left">
                            {entry.similarQuestions.map((question) => (
                              <li key={question} className="whitespace-pre-wrap break-words">
                                {question}
                              </li>
                            ))}
                          </ul>
                        </TooltipContent>
                      </Tooltip>
                    ) : null}
                  </span>
                </span>
              </div>
            ),
          },
          {
            key: "answer",
            header: t("qa.answer"),
            cellClassName: "max-w-0 text-muted-foreground",
            cell: (entry) => <span className="block truncate">{entry.answer}</span>,
          },
          {
            key: "createdAt",
            header: t("qa.updatedAt"),
            cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
            cell: (entry) =>
              t("qa.updatedAtTime", { time: formatDateTime(entry.updatedAt) }),
          },
        ]}
        rows={entries}
        rowKey={(entry) => entry.id}
        empty={filtered ? t("qa.filteredEmpty") : t("qa.empty")}
        onRowActivate={(entry) => navigate(returnLink(`${listPath}/${entry.id}/edit`))}
        rowActions={(entry) => [
          {
            key: "reprocess",
            label: t("qa.reprocess"),
            disabled: retryingIds.has(entry.id),
            onSelect: () => retry.mutate(entry.id),
          },
          {
            key: "delete",
            label: t("common:actions.delete"),
            destructive: true,
            separatorBefore: true,
            onSelect: () => onDelete(entry),
          },
        ]}
      />
    </ResourceListFrame>
  )
}
