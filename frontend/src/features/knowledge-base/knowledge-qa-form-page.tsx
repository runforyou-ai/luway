/** 本地问答的独立新增和编辑页面。 */
import { useEffect, useMemo } from "react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  createKnowledgeQAEntry,
  getKnowledgeBase,
  getKnowledgeQAEntry,
  KnowledgeBaseCategory,
  updateKnowledgeQAEntry,
  type KnowledgeBaseData,
  type KnowledgeQAEntryData,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import {
  createQASchema,
  QAFormFields,
  type QAFormValues,
} from "@/components/knowledge-qa-fields"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 读取知识库和问答详情后展示编辑表单。 */
export function KnowledgeQAFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation("knowledgeBase")
  const { knowledgeBaseId = "", entryId = "" } = useParams()
  const location = useLocation()
  const base = useResource(
    resourceKeys.knowledgeBase(knowledgeBaseId),
    (signal) => getKnowledgeBase(knowledgeBaseId, signal),
  )
  const entry = useResource(
    resourceKeys.knowledgeQAEntry(knowledgeBaseId, entryId),
    (signal) => getKnowledgeQAEntry(knowledgeBaseId, entryId, signal),
    { enabled: mode === "edit", staleTime: 0 },
  )
  const supported =
    base.data?.category === KnowledgeBaseCategory.KnowledgeBaseCategoryQA
  return (
    <>
      <PageHeader
        title={t(mode === "create" ? "qa.createTitle" : "qa.editTitle")}
        description={t(
          mode === "create" ? "qa.createDescription" : "qa.editDescription",
        )}
        backTo={mode === "edit" ? `/knowledge-bases/${knowledgeBaseId}/qa${location.search}` : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={mode === "edit" ? [base, entry] : [base]}
          errorMessage={t("qa.loadError")}
        >
          {!supported ? (
            <p className="text-sm text-muted-foreground">{t("qa.unsupported")}</p>
          ) : (
            <KnowledgeQAForm
              key={`${knowledgeBaseId}/${entryId}/${mode}`}
              knowledgeBase={base.data!}
              entry={mode === "edit" ? entry.data : undefined}
            />
          )}
        </ResourceContent>
      </PageContent>
    </>
  )
}

/** 保存完整问答并返回原列表的筛选和页码。 */
function KnowledgeQAForm({
  knowledgeBase,
  entry,
}: {
  knowledgeBase: KnowledgeBaseData
  entry?: KnowledgeQAEntryData
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const navigate = useNavigate()
  const location = useLocation()
  const invalidate = useResourceInvalidator()
  const schema = useMemo(
    () =>
      createQASchema({
        question: t("qa.questionRequired"),
        answer: t("qa.answerRequired"),
      }),
    [t],
  )
  const form = useForm<QAFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存；新建时等提交再校验，避免原生校验把焦点锁在必填项上。
    mode: entry ? "onBlur" : "onSubmit",
    defaultValues: {
      question: entry?.question ?? "",
      answer: entry?.answer ?? "",
      similarQuestions: entry?.similarQuestions ?? [],
    },
  })
  const returnPath = `/knowledge-bases/${knowledgeBase.id}/qa${location.search}`
  useEffect(() => {
    if (!entry || form.formState.isDirty) return
    form.reset({
      question: entry.question,
      answer: entry.answer,
      similarQuestions: entry.similarQuestions,
    })
  }, [entry, form])

  /** 提交表单并失效该知识库下的问答缓存。 */
  // 编辑已有问答时边改边存，新建仍由底部按钮提交并跳回列表。
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: Boolean(entry),
    save: async (values) => {
      const saved = entry
        ? await updateKnowledgeQAEntry(knowledgeBase.id, entry.id, values)
        : await createKnowledgeQAEntry(knowledgeBase.id, values)
      await Promise.all([
        invalidate(resourceKeys.knowledgeQAEntries(knowledgeBase.id)),
        invalidate(resourceKeys.knowledgeQAEntry(knowledgeBase.id, saved.id)),
      ])
      return saved
    },
    onSubmitted: () => {
      toast.success(t("qa.saveSuccess"))
      navigate(returnPath, { replace: true })
    },
    errorMessage: t("qa.saveError"),
    logLabel: "保存问答",
  })

  return (
    <form
      className="w-full space-y-9"
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <QAFormFields
        control={form.control}
        disabled={form.formState.isSubmitting}
      />
      {entry ? null : (
        <FormActions
          saving={form.formState.isSubmitting}
          onCancel={() => navigate(returnPath, { replace: true })}
        />
      )}
    </form>
  )
}
