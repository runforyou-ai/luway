/** 展示知识检索命中的文档分段或问答条目及匹配度。 */
import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import { KnowledgeBaseCategory, type KnowledgeBaseCategoryId, type KnowledgeRetrievalResultData } from "@/api"
import { SelectableText } from "@/components/selectable-text"
import { Button } from "@/components/ui/button"

/** 按最终顺序展示命中内容；文档分段提供查看上下文入口，问答条目展示命中片段和完整答案。 */
export function KnowledgeRetrievalResults({ category, records, onViewContext }: {
  category: KnowledgeBaseCategoryId
  records: KnowledgeRetrievalResultData["records"]
  onViewContext: (record: KnowledgeRetrievalResultData["records"][number], trigger: HTMLButtonElement) => void
}) {
  const { t, i18n } = useTranslation("knowledgeBase")
  const qa = category === KnowledgeBaseCategory.KnowledgeBaseCategoryQA
  const scoreFormatter = useMemo(
    () => new Intl.NumberFormat(i18n.resolvedLanguage, { maximumFractionDigits: 3 }),
    [i18n.resolvedLanguage],
  )
  return (
    <div>
      <p className="mb-1 text-sm text-muted-foreground">{t("retrieval.resultCount", { count: records.length })}</p>
      <ol className="divide-y">
        {records.map((record, index) => (
          <li key={record.segmentId} className="py-5">
            <div className="flex items-start gap-3">
              <span className="w-5 shrink-0 text-right text-sm font-medium tabular-nums">{index + 1}</span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                  <SelectableText className="break-all text-sm font-medium">{record.documentName}</SelectableText>
                  {!qa && (
                    <span className="inline-flex items-baseline gap-2 text-xs text-muted-foreground">
                      <span>{t("retrieval.position", { position: record.position })}</span>
                      <Button type="button" variant="link" className="h-auto p-0 text-xs font-normal" onClick={(event) => onViewContext(record, event.currentTarget)}>
                        {t("retrieval.viewContext")}
                      </Button>
                    </span>
                  )}
                </div>
                {qa ? (
                  <dl className="mt-3 grid gap-3 text-sm leading-6">
                    <div>
                      <dt className="text-xs text-muted-foreground">{t("retrieval.matched")}</dt>
                      <dd><SelectableText className="block whitespace-pre-wrap break-words">{record.content}</SelectableText></dd>
                    </div>
                    <div>
                      <dt className="text-xs text-muted-foreground">{t("retrieval.answer")}</dt>
                      <dd><SelectableText className="block whitespace-pre-wrap break-words">{record.answer}</SelectableText></dd>
                    </div>
                  </dl>
                ) : (
                  <div className="mt-3 space-y-1">
                    {record.context && <SelectableText className="block whitespace-pre-wrap break-words text-xs leading-5 text-muted-foreground">{record.context}</SelectableText>}
                    <SelectableText className="block whitespace-pre-wrap break-words text-sm leading-6">{record.content}</SelectableText>
                  </div>
                )}
              </div>
              <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                {t("retrieval.score", { score: scoreFormatter.format(record.score) })}
              </span>
            </div>
          </li>
        ))}
      </ol>
    </div>
  )
}
