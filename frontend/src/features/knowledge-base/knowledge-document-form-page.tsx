/** 在线文档的独立新增和编辑页面。 */
import { useEffect, useId, useMemo, useState } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate, useParams } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  createKnowledgeTextDocument,
  getKnowledgeBase,
  getKnowledgeDocumentContent,
  KnowledgeBaseCategory,
  KnowledgeDocumentSourceKind,
  updateKnowledgeDocumentContent,
  type KnowledgeDocumentContentData,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"
import { KnowledgeDocumentEditor } from "./knowledge-document-editor"
import { knowledgeDocumentTitleMaxLength } from "./knowledge-base-schema"

/** 生成在线文档表单的必填与长度校验。 */
function createDocumentSchema(messages: {
  title: string
  titleTooLong: string
  content: string
}) {
  return z.object({
    title: z
      .string()
      .trim()
      .min(1, messages.title)
      .max(knowledgeDocumentTitleMaxLength, messages.titleTooLong),
    content: z.string().trim().min(1, messages.content),
  })
}

type DocumentFormValues = z.infer<ReturnType<typeof createDocumentSchema>>

/** 读取知识库和文档正文后展示编辑表单。 */
export function KnowledgeDocumentFormPage({
  mode,
}: {
  mode: "create" | "edit"
}) {
  const { t } = useTranslation("knowledgeBase")
  const {
    knowledgeBaseId = "",
    documentId = "",
  } = useParams()
  const location = useLocation()
  const base = useResource(
    resourceKeys.knowledgeBase(knowledgeBaseId),
    (signal) => getKnowledgeBase(knowledgeBaseId, signal),
  )
  const content = useResource(
    resourceKeys.knowledgeDocumentContent(knowledgeBaseId, documentId),
    (signal) => getKnowledgeDocumentContent(knowledgeBaseId, documentId, signal),
    { enabled: mode === "edit", staleTime: 0 },
  )
  const supported =
    base.data?.category ===
      KnowledgeBaseCategory.KnowledgeBaseCategoryStandard &&
    (mode === "create" ||
      content.data?.document.sourceKind ===
        KnowledgeDocumentSourceKind.KnowledgeDocumentSourceText)
  return (
    <>
      <PageHeader
        title={t(
          mode === "create" ? "documents.createTitle" : "documents.editTitle",
        )}
        description={t(
          mode === "create"
            ? "documents.createDescription"
            : "documents.editDescription",
        )}
        backTo={mode === "edit" ? `/knowledge-bases/${knowledgeBaseId}/documents${location.search}` : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={mode === "edit" ? [base, content] : [base]}
          errorMessage={t("documents.loadError")}
        >
          {!supported ? (
            <p className="text-sm text-muted-foreground">
              {t("documents.sourceUnsupported")}
            </p>
          ) : (
            <KnowledgeDocumentForm
              key={`${knowledgeBaseId}/${documentId}/${mode}`}
              baseId={knowledgeBaseId}
              stored={mode === "edit" ? content.data : undefined}
            />
          )}
        </ResourceContent>
      </PageContent>
    </>
  )
}

/** 保存在线文档并返回原列表的筛选和页码。 */
function KnowledgeDocumentForm({
  baseId,
  stored,
}: {
  baseId: string
  stored?: KnowledgeDocumentContentData
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const navigate = useNavigate()
  const location = useLocation()
  const invalidate = useResourceInvalidator()
  const id = useId()
  const schema = useMemo(
    () =>
      createDocumentSchema({
        title: t("documents.titleRequired"),
        titleTooLong: t("documents.titleTooLong"),
        content: t("documents.contentRequired"),
      }),
    [t],
  )
  const form = useForm<DocumentFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存；新建时等提交再校验，避免原生校验把焦点锁在必填项上。
    mode: stored ? "onBlur" : "onSubmit",
    defaultValues: {
      title: stored?.document.name ?? "",
      content: stored?.content ?? "",
    },
  })
  const returnPath = `/knowledge-bases/${baseId}/documents${location.search}`
  // 表单未编辑时跟随最新读取到的名称与正文，编辑器按内容版本重建。
  const [contentVersion, setContentVersion] = useState(0)
  useEffect(() => {
    if (!stored || form.formState.isDirty) return
    const values = form.getValues()
    if (values.title === stored.document.name && values.content === stored.content)
      return
    form.reset({ title: stored.document.name, content: stored.content })
    setContentVersion((version) => version + 1)
  }, [stored, form])

  /** 提交表单并失效该文档的列表、详情和正文缓存。 */
  // 编辑已有文档时边改边存，新建仍由底部按钮提交并跳回列表。
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: Boolean(stored),
    save: async (values) => {
      const saved = stored
        ? await updateKnowledgeDocumentContent(baseId, stored.document.id, values)
        : await createKnowledgeTextDocument(baseId, values)
      await Promise.all([
        invalidate(resourceKeys.knowledgeDocuments(baseId)),
        invalidate(resourceKeys.knowledgeDocument(baseId, saved.id)),
        invalidate(resourceKeys.knowledgeDocumentContent(baseId, saved.id)),
      ])
    },
    onSubmitted: () => {
      toast.success(t("documents.saveSuccess"))
      navigate(returnPath, { replace: true })
    },
    errorMessage: t("documents.saveError"),
    logLabel: "保存在线文档",
  })

  const disabled = form.formState.isSubmitting
  return (
    <form
      className="w-full space-y-9"
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        <FormInputField
          control={form.control}
          name="title"
          id={`${id}-title`}
          label={t("documents.name")}
          required
          disabled={disabled}
        />
        <Controller
          control={form.control}
          name="content"
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor={`${id}-content`} required>
                {t("documents.content")}
              </FieldLabel>
              <KnowledgeDocumentEditor
                key={contentVersion}
                id={`${id}-content`}
                value={field.value}
                disabled={disabled}
                onChange={field.onChange}
                fieldRef={field.ref}
              />
            </Field>
          )}
        />
      </FieldGroup>
      {stored ? null : (
        <FormActions
          saving={disabled}
          onCancel={() => navigate(returnPath, { replace: true })}
        />
      )}
    </form>
  )
}
