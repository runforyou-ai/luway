/** 待补知识的问答表单：同步 AI 草稿，可并入知识库中召回的相似问答。 */
import { useEffect, useId, useRef, useState, type ReactNode } from "react"
import { useForm, type UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  acceptKnowledgeGap,
  getKnowledgeQAEntry,
  isApiError,
  KnowledgeGapDraftStatus,
  retrieveKnowledgeBase,
  sessionPath,
  type KnowledgeBase,
  type KnowledgeGap,
} from "@/api"
import { QAFormFields, qaSchema, type QAFormValues } from "@/components/knowledge-qa-fields"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceReader } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

import { useKnowledgeGapRefresh } from "./use-knowledge-gap-actions"

/** 知识库召回内容的最大字符数。 */
const similarQueryMaxLength = 250

/** 并入的已有问答编号与标准问题。 */
type MergeTarget = { entryId: string; question: string }

/** 由条目生成表单值：草稿就绪时取 AI 草稿，否则取客户提问原文。 */
function draftValues(gap: KnowledgeGap): QAFormValues {
  return {
    question: gap.draft?.question || gap.question,
    similarQuestions: (gap.draft?.similarQuestions ?? []).map((content) => ({ id: "", content })),
    answer: gap.draft?.answer ?? "",
  }
}

/** 编辑问答并加入知识库：草稿变化时同步未改动的表单，可并入知识库中召回的相似问答。 */
export function KnowledgeGapForm({
  gap,
  bases,
  dismissButton,
  dismissing,
  onHandled,
}: {
  gap: KnowledgeGap
  bases: KnowledgeBase[]
  dismissButton: ReactNode
  dismissing: boolean
  onHandled: () => void
}) {
  const { t } = useTranslation(["agents", "knowledgeBase"])
  const id = useId()
  const refresh = useKnowledgeGapRefresh()
  const form = useForm<QAFormValues>({
    resolver: zodResolver(qaSchema),
    shouldUseNativeValidation: true,
    defaultValues: draftValues(gap),
  })
  const [knowledgeBaseId, setKnowledgeBaseId] = useState(
    bases.some((item) => item.id === gap.defaultKnowledgeBaseId)
      ? gap.defaultKnowledgeBaseId
      : (bases[0]?.id ?? ""),
  )
  const [merge, setMerge] = useState<MergeTarget | null>(null)
  const [draftArrived, setDraftArrived] = useState(false)
  // 提问可以加入接待 AI 员工的评测时默认同时加入。
  const evaluable = gap.evaluable
  const [addToEvaluation, setAddToEvaluation] = useState(true)
  const merging = useMergeInto(form, gap, knowledgeBaseId, setMerge)
  const { submit } = useFormSave({
    form,
    schema: qaSchema,
    autoSave: false,
    save: (values) =>
      acceptKnowledgeGap(gap.id, {
        knowledgeBaseId,
        entryId: merge?.entryId ?? "",
        entry: values,
        addToEvaluation: evaluable && addToEvaluation,
      }),
    onSubmitted: () => {
      toast.success(t("performance.gapSheet.acceptSuccess"))
      onHandled()
      refresh(gap.id, knowledgeBaseId)
    },
    errorMessage: t("performance.gapSheet.acceptError"),
    errorFields: ["question", "similarQuestions", "answer"],
    logLabel: "加入知识库",
  })
  const appliedDraft = useRef(JSON.stringify(gap.draft))
  useEffect(() => {
    // 草稿变化时同步未改动的表单；表单已改动或已并入时，新草稿到达后提示替换。
    const current = JSON.stringify(gap.draft)
    if (current === appliedDraft.current) return
    appliedDraft.current = current
    if (!form.formState.isDirty && !merge) {
      form.reset(draftValues(gap))
      setDraftArrived(false)
    } else if (gap.draft) {
      setDraftArrived(true)
    }
  }, [gap, form, merge])
  const query = (gap.draft?.question || gap.question).trim().slice(0, similarQueryMaxLength)
  const similar = useResource(
    resourceKeys.knowledgeGapSimilarQA(gap.id, knowledgeBaseId, query),
    () => retrieveKnowledgeBase(knowledgeBaseId, { query }),
    { enabled: Boolean(knowledgeBaseId && query), staleTime: 0 },
  )
  const busy = form.formState.isSubmitting || dismissing || merging.loading

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <div className="space-y-6">
        <KnowledgeGapDraftNotice
          gap={gap}
          draftArrived={draftArrived}
          disabled={busy}
          onUseDraft={() => {
            form.reset(draftValues(gap))
            setMerge(null)
            setDraftArrived(false)
          }}
        />
        <Field>
          <FieldLabel htmlFor={`${id}-knowledge-base`} required>
            {t("performance.gapSheet.knowledgeBase")}
          </FieldLabel>
          <NativeSelect
            id={`${id}-knowledge-base`}
            value={knowledgeBaseId}
            required
            disabled={busy || merge !== null}
            onChange={(event) => setKnowledgeBaseId(event.target.value)}
          >
            {bases.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <KnowledgeGapMergeHint
          merge={merge}
          match={similar.data?.records[0]?.content ?? ""}
          disabled={busy}
          onMerge={() => {
            const match = similar.data?.records[0]
            if (match) void merging.mergeInto(match.documentId)
          }}
          onCreateNew={() => {
            form.reset(draftValues(gap))
            setMerge(null)
          }}
        />
        <QAFormFields control={form.control} disabled={busy} answerRows={8} />
        {evaluable ? (
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              className="size-4 accent-primary"
              checked={addToEvaluation}
              disabled={busy}
              onChange={(event) => setAddToEvaluation(event.target.checked)}
            />
            {t("performance.gapSheet.addToEvaluation")}
          </label>
        ) : null}
      </div>
      <div className="flex justify-end gap-2">
        {dismissButton}
        <Button type="submit" disabled={busy}>
          {form.formState.isSubmitting
            ? t("performance.gapSheet.accepting")
            : t("performance.gapSheet.accept")}
        </Button>
      </div>
    </form>
  )
}

/** 说明 AI 起草的进度：起草中、失败、未设置小结模型，或草稿在表单改动后到达。 */
function KnowledgeGapDraftNotice({
  gap,
  draftArrived,
  disabled,
  onUseDraft,
}: {
  gap: KnowledgeGap
  draftArrived: boolean
  disabled: boolean
  onUseDraft: () => void
}) {
  const { t } = useTranslation("agents")
  if (draftArrived)
    return (
      <p className="flex flex-wrap items-center gap-x-2 text-sm text-muted-foreground">
        {t("performance.gapSheet.draftArrived")}
        <Button type="button" variant="link" size="sm" className="h-auto px-0" disabled={disabled} onClick={onUseDraft}>
          {t("performance.gapSheet.useDraft")}
        </Button>
      </p>
    )
  const notice = {
    [KnowledgeGapDraftStatus.Pending]: t("performance.gapSheet.drafting"),
    [KnowledgeGapDraftStatus.Failed]: t("performance.gapSheet.draftFailed"),
    [KnowledgeGapDraftStatus.Unavailable]: t("performance.gapSheet.draftUnavailable"),
  }[gap.draftStatus as string]
  return notice ? <p className="text-sm text-muted-foreground">{notice}</p> : null
}

/** 并入已有问答时提示将更新的问答并可改为新建，否则在召回到相似问答时提示并入。 */
function KnowledgeGapMergeHint({
  merge,
  match,
  disabled,
  onMerge,
  onCreateNew,
}: {
  merge: MergeTarget | null
  match: string
  disabled: boolean
  onMerge: () => void
  onCreateNew: () => void
}) {
  const { t } = useTranslation("agents")
  if (!merge && !match) return null
  return (
    <p className="flex flex-wrap items-center gap-x-2 text-sm text-muted-foreground">
      {merge
        ? t("performance.gapSheet.merging", { question: merge.question })
        : t("performance.gapSheet.similar", { question: match })}
      <Button
        type="button"
        variant="link"
        size="sm"
        className="h-auto px-0"
        disabled={disabled}
        onClick={merge ? onCreateNew : onMerge}
      >
        {t(merge ? "performance.gapSheet.createNew" : "performance.gapSheet.merge")}
      </Button>
    </p>
  )
}

/** 读取召回的已有问答并填入表单，把本条问题追加为相似问题；读取期间知识库已切换时丢弃结果。 */
function useMergeInto(
  form: UseFormReturn<QAFormValues>,
  gap: KnowledgeGap,
  knowledgeBaseId: string,
  onMerged: (target: MergeTarget) => void,
) {
  const { t } = useTranslation("agents")
  const read = useResourceReader()
  const [loading, setLoading] = useState(false)
  const currentBase = useRef(knowledgeBaseId)
  currentBase.current = knowledgeBaseId

  /** 并入指定问答。 */
  async function mergeInto(entryId: string) {
    const requestedBase = knowledgeBaseId
    setLoading(true)
    try {
      const entry = await read(resourceKeys.knowledgeQAEntry(requestedBase, entryId), (signal) =>
        getKnowledgeQAEntry(requestedBase, entryId, signal),
      )
      if (currentBase.current !== requestedBase) return
      const values = form.getValues()
      const existing = new Set([entry.question, ...entry.similarQuestions.map((item) => item.content)])
      const added = [values.question, ...values.similarQuestions.map((item) => item.content)]
        .map((content) => content.trim())
        .filter((content) => content && !existing.has(content))
      form.reset({
        question: entry.question,
        similarQuestions: [...entry.similarQuestions, ...added.map((content) => ({ id: "", content }))],
        answer: entry.answer,
      })
      onMerged({ entryId: entry.id, question: entry.question })
    } catch (error) {
      if (isApiError(error) && sessionPath(error.state)) return
      console.warn("读取已有问答失败", { gapId: gap.id, error })
      toast.error(t("performance.gapSheet.mergeError"))
    } finally {
      setLoading(false)
    }
  }

  return { loading, mergeInto }
}
