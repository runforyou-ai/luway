/** 知识库检索测试侧栏，按知识库类别展示文档分段或问答条目。 */
import { useRef, useState, type RefObject } from "react"
import { useMutation } from "@tanstack/react-query"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { z } from "zod"

import { isApiError, retrieveKnowledgeBase, sessionPath, type KnowledgeBaseCategory, type KnowledgeRetrievalResult } from "@/api"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"
import { KnowledgeRetrievalResults } from "./knowledge-retrieval-results"
import { KnowledgeSegmentsDialog } from "./knowledge-segments-dialog"

/** 检索内容的最大字符数。 */
const retrievalQueryMaxLength = 250

/** 检索表单校验规则。 */
const retrievalSchema = z.object({
  query: z.string().trim().min(1).max(retrievalQueryMaxLength),
})

/** 显示检索输入和按最终顺序排列的命中内容。 */
export function KnowledgeRetrievalSheet({ open, onOpenChange, knowledgeBaseId, category, triggerRef }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  knowledgeBaseId: string
  category: KnowledgeBaseCategory
  triggerRef: RefObject<HTMLButtonElement | null>
}) {
  const { t } = useTranslation("knowledgeBase")
  const navigate = useNavigate()
  // 检索状态只反映最后一次请求。
  const retrieval = useMutation({
    mutationFn: (values: { query: string }) => retrieveKnowledgeBase(knowledgeBaseId, values),
  })
  const result = retrieval.data ?? null
  // 会话失效的错误转入会话入口，不在侧栏展示。
  const requestError = isApiError(retrieval.error) && sessionPath(retrieval.error.state) ? null : retrieval.error
  const loading = retrieval.isPending
  const [contextRecord, setContextRecord] = useState<KnowledgeRetrievalResult["records"][number] | null>(null)
  const contextTrigger = useRef<HTMLButtonElement | null>(null)
  const form = useForm<z.infer<typeof retrievalSchema>>({
    resolver: zodResolver(retrievalSchema),
    shouldUseNativeValidation: true,
    defaultValues: { query: "" },
  })
  /** 发起检索，只采纳最后一次请求的结果。 */
  function retrieve(values: z.infer<typeof retrievalSchema>) {
    retrieval.mutate(values, { onError: (error) => recoverSession(error, navigate) })
  }

  return (
    <>
      <Sheet
        open={open}
        onOpenChange={(next) => {
          if (!next) setContextRecord(null)
          onOpenChange(next)
        }}
      >
        <SheetContent
          className="w-full gap-0 p-0 sm:max-w-xl"
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            form.setFocus("query")
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            triggerRef.current?.focus()
          }}
        >
          <SheetHeader className="px-6 pt-6 pr-12 pb-0">
            <SheetTitle>{t("retrieval.title")}</SheetTitle>
            <SheetDescription>{t("retrieval.description")}</SheetDescription>
          </SheetHeader>
          <div className="flex min-h-0 flex-1 flex-col">
            <form className="shrink-0 space-y-9 px-6 pt-4 pb-6" onSubmit={form.handleSubmit(retrieve)} noValidate>
              <FieldGroup className="gap-5">
                <Controller
                  name="query"
                  control={form.control}
                  render={({ field, fieldState }) => (
                    <Field data-invalid={fieldState.invalid}>
                      <FieldLabel htmlFor={field.name} required>{t("retrieval.query")}</FieldLabel>
                      <Input {...field} id={field.name} autoComplete="off" maxLength={retrievalQueryMaxLength} required aria-invalid={fieldState.invalid} />
                    </Field>
                  )}
                />
              </FieldGroup>
              <Button type="submit" disabled={loading}>{t("retrieval.submit")}</Button>
            </form>
            <div className="min-h-0 flex-1 overflow-y-auto border-t px-6 py-5" aria-live="polite" aria-busy={loading}>
              {loading ? (
                <p className="py-12 text-center text-sm text-muted-foreground">{t("retrieval.loading")}</p>
              ) : requestError ? (
                <p className="py-12 text-center text-sm text-destructive">
                  {isApiError(requestError) ? apiErrorMessage(requestError, ["query"]) : t("retrieval.error")}
                </p>
              ) : !result ? (
                <p className="py-12 text-center text-sm text-muted-foreground">{t("retrieval.initial")}</p>
              ) : result.records.length === 0 ? (
                <p className="py-12 text-center text-sm text-muted-foreground">{t("retrieval.empty")}</p>
              ) : (
                <KnowledgeRetrievalResults
                  category={category}
                  records={result.records}
                  onViewContext={(record, trigger) => {
                    contextTrigger.current = trigger
                    setContextRecord(record)
                  }}
                />
              )}
            </div>
          </div>
        </SheetContent>
      </Sheet>
      {contextRecord && (
        <KnowledgeSegmentsDialog
          key={contextRecord.segmentId}
          knowledgeBaseId={knowledgeBaseId}
          documentId={contextRecord.documentId}
          documentName={contextRecord.documentName}
          segmentId={contextRecord.segmentId}
          segmentBatchId={contextRecord.segmentBatchId}
          triggerRef={contextTrigger}
          onClose={() => setContextRecord(null)}
        />
      )}
    </>
  )
}
