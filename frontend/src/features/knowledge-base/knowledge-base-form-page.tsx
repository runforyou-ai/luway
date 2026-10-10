/** 企业知识库新建和编辑页。 */
import { useEffect, useMemo, useRef, useState } from "react"
import { Controller, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router"
import { toast } from "sonner"

import {
  AIModelUsage,
  KnowledgeBaseCategory,
  createKnowledgeBase,
  getKnowledgeBase,
  listKnowledgeBaseAgents,
  updateKnowledgeBase,
  UserStatus,
} from "@/api"
import { useAIModelOptions } from "@/components/ai-model-options"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { LoadingIndicator } from "@/components/loading-indicator"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import {
  createKnowledgeBaseSchema,
  knowledgeBaseDescriptionMaxLength,
  knowledgeBaseNameMaxLength,
  type KnowledgeBaseFormValues,
} from "@/features/knowledge-base/knowledge-base-schema"
import { KnowledgeBaseSettingsFields } from "./knowledge-base-settings-fields"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 创建或编辑知识库。 */
export function KnowledgeBaseFormPage({
  mode,
}: {
  mode: "create" | "edit"
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const requestedCategory =
    searchParams.get("category") ===
    KnowledgeBaseCategory.QA
      ? KnowledgeBaseCategory.QA
      : KnowledgeBaseCategory.Standard
  const { knowledgeBaseId = "" } = useParams()
  const invalidateResource = useResourceInvalidator()
  const [category, setCategory] =
    useState<KnowledgeBaseCategory>(requestedCategory)
  const saveButton = useRef<HTMLButtonElement>(null)
  const [confirmReindex, setConfirmReindex] = useState(false)
  const isQA = category === KnowledgeBaseCategory.QA
  const schema = useMemo(
    () =>
      createKnowledgeBaseSchema({
        embeddingDimensionInvalid: t("validation.embeddingDimensionInvalid"),
        chunkLengthInvalid: t("validation.chunkLengthInvalid"),
        chunkOverlapInvalid: t("validation.chunkOverlapInvalid"),
        retrievalCountInvalid: t("validation.retrievalCountInvalid"),
        retrievalScoreThresholdInvalid: t("validation.retrievalScoreThresholdInvalid"),
      }, isQA),
    [t, isQA],
  )
  const form = useForm<KnowledgeBaseFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存，新建时只在提交时校验。
    mode: mode === "edit" ? "onBlur" : "onSubmit",
    defaultValues: {
      name: "",
      description: "",
      embeddingModelId: "",
      embeddingDimension: "",
      chunkLength: "512",
      chunkOverlap: "50",
      retrievalCount: "3",
      retrievalScoreThreshold: "0.5",
      rerankModelId: "",
    },
  })
  useEffect(() => {
    if (mode !== "create") return
    setCategory(requestedCategory)
    form.reset({
      name: "",
      description: "",
      embeddingModelId: "",
      embeddingDimension: "",
      chunkLength: "512",
      chunkOverlap: "50",
      retrievalCount: "3",
      retrievalScoreThreshold: "0.5",
      rerankModelId: "",
    })
  }, [form, mode, requestedCategory])

  const detail = useResource(
    resourceKeys.knowledgeBase(knowledgeBaseId),
    () => getKnowledgeBase(knowledgeBaseId),
    { enabled: mode === "edit" },
  )
  const loadedKnowledgeBase = detail.data
  const agents = useResource(
    resourceKeys.knowledgeBaseAgents(knowledgeBaseId),
    () => listKnowledgeBaseAgents(knowledgeBaseId),
    { enabled: mode === "edit", staleTime: 0 },
  )
  const embeddingModels = useAIModelOptions(AIModelUsage.Embedding)
  const rerankModels = useAIModelOptions(AIModelUsage.Rerank)
  const initializedDetail = useRef<string | null>(null)
  // 详情就绪后回填知识库表单和派生状态。
  useEffect(() => {
    if (!loadedKnowledgeBase) return
    if (initializedDetail.current === knowledgeBaseId && form.formState.isDirty) return
    initializedDetail.current = knowledgeBaseId
    const values = {
      name: loadedKnowledgeBase.name,
      description: loadedKnowledgeBase.description,
      embeddingModelId: loadedKnowledgeBase.embeddingModelId,
      embeddingDimension: String(loadedKnowledgeBase.embeddingDimension),
      chunkLength: loadedKnowledgeBase.chunkLength === null ? "" : String(loadedKnowledgeBase.chunkLength),
      chunkOverlap: loadedKnowledgeBase.chunkOverlap === null ? "" : String(loadedKnowledgeBase.chunkOverlap),
      retrievalCount: String(loadedKnowledgeBase.retrievalCount),
      retrievalScoreThreshold: String(loadedKnowledgeBase.retrievalScoreThreshold),
      rerankModelId: loadedKnowledgeBase.rerankModelId,
    }
    form.reset(values)
    markSaved(values)
    setCategory(loadedKnowledgeBase.category)
  }, [form, loadedKnowledgeBase])

  const cancelPath = "/knowledge-bases"

  // 保存知识库：编辑已有知识库时边改边存；变更向量模型、维度或分段参数时先确认重建索引。
  const reindexAutoSaved = useRef(false)
  const watchedValues = useWatch({ control: form.control })
  const { submit, commit, markSaved } = useFormSave({
    form,
    schema,
    autoSave: mode === "edit",
    // 需要确认重建索引的改动在确认前不会保存，离开页面时提示放弃。
    unsaved: requiresReindex(watchedValues),
    hold: (values, autoSaved) => {
      if (!requiresReindex(values)) return false
      reindexAutoSaved.current = autoSaved
      setConfirmReindex(true)
      return true
    },
    save: async (values) => {
      // 按知识库类型提交分段配置。
      const input = {
        name: values.name,
        description: values.description,
        category,
        embeddingModelId: values.embeddingModelId,
        embeddingDimension: Number(values.embeddingDimension),
        chunkLength: isQA ? null : Number(values.chunkLength),
        chunkOverlap: isQA ? null : Number(values.chunkOverlap),
        retrievalCount: Number(values.retrievalCount),
        retrievalScoreThreshold: Number(values.retrievalScoreThreshold),
        rerankModelId: values.rerankModelId,
      }
      if (mode === "create") return createKnowledgeBase(input)
      const knowledgeBase = await updateKnowledgeBase(knowledgeBaseId, input)
      void invalidateResource(resourceKeys.knowledgeBase(knowledgeBaseId))
      return knowledgeBase
    },
    onSaved: () => {
      setConfirmReindex(false)
      void invalidateResource(resourceKeys.knowledgeBases())
    },
    onSubmitted: () => {
      toast.success(t("form.createSuccess"))
      navigate("/knowledge-bases")
    },
    errorMessage: t("form.saveError"),
    errorFields: [
      "name",
      "category",
      "description",
      "embeddingModelId",
      "embeddingDimension",
      "chunkLength",
      "chunkOverlap",
      "retrievalCount",
      "retrievalScoreThreshold",
      "rerankModelId",
    ],
    logLabel: "知识库保存",
  })

  /** 编辑页变更向量模型、维度或分段参数时需要重建索引。 */
  function requiresReindex(values: Partial<KnowledgeBaseFormValues>) {
    return Boolean(
      mode === "edit" &&
        loadedKnowledgeBase &&
        (values.embeddingModelId !== loadedKnowledgeBase.embeddingModelId ||
          Number(values.embeddingDimension) !== loadedKnowledgeBase.embeddingDimension ||
          (!isQA && (Number(values.chunkLength) !== loadedKnowledgeBase.chunkLength || Number(values.chunkOverlap) !== loadedKnowledgeBase.chunkOverlap))),
    )
  }

  const title =
    mode === "create" ? t("form.createTitle") : t("form.editTitle")

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={title}
        description={t(
          mode === "create"
            ? "form.createDescription"
            : "form.editDescription",
        )}
        backTo={mode === "edit" ? cancelPath : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={mode === "edit" ? [embeddingModels, rerankModels, detail] : [embeddingModels, rerankModels]}
          errorMessage={t("form.loadError")}
        >
          <form
            className="w-full space-y-9"
            onSubmit={form.handleSubmit(submit)}
            noValidate
          >
            <FieldGroup>
              <Field>
                <FieldLabel>{t("form.category")}</FieldLabel>
                <p className="text-sm">
                  {category === KnowledgeBaseCategory.QA
                    ? t("category.qa")
                    : t("category.standard")}
                </p>
              </Field>
              {mode === "edit" ? (
                <Field>
                  <FieldLabel>{t("agents.title")}</FieldLabel>
                  {agents.loading || agents.retrying ? (
                    <LoadingIndicator>{t("common:status.loading")}</LoadingIndicator>
                  ) : agents.error ? (
                    <p className="flex items-center gap-2 text-sm text-muted-foreground">
                      {t("agents.loadError")}
                      <Button
                        type="button"
                        variant="link"
                        size="xs"
                        className="h-auto p-0"
                        onClick={() => void agents.refresh()}
                      >
                        {t("common:actions.retry")}
                      </Button>
                    </p>
                  ) : agents.data?.agents.length ? (
                    <p className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
                      {agents.data.agents.map((agent) => (
                        <Link
                          key={agent.id}
                          to={`/ai-employees/${agent.id}`}
                          className="underline-offset-4 hover:underline"
                        >
                          {agent.status === UserStatus.Active
                            ? agent.displayName
                            : t("agents.inactive", { name: agent.displayName })}
                        </Link>
                      ))}
                    </p>
                  ) : (
                    <p className="text-sm text-muted-foreground">{t("agents.empty")}</p>
                  )}
                </Field>
              ) : null}
              <FormInputField
                name="name"
                control={form.control}
                label={t("form.name")}
                autoFocus
                maxLength={knowledgeBaseNameMaxLength}
              />
              <Controller
                name="description"
                control={form.control}
                render={({ field, fieldState }) => (
                  <Field data-invalid={fieldState.invalid}>
                    <FieldLabel
                      htmlFor="knowledge-base-description"
                      required={false}
                    >
                      {t("form.description")}
                    </FieldLabel>
                    <Textarea
                      {...field}
                      id="knowledge-base-description"
                      rows={6}
                      maxLength={knowledgeBaseDescriptionMaxLength}
                      aria-invalid={fieldState.invalid}
                    />
                  </Field>
                )}
              />
              <KnowledgeBaseSettingsFields control={form.control} isQA={isQA} embeddingModels={embeddingModels.data ?? []} rerankModels={rerankModels.data ?? []} />
            </FieldGroup>
            {mode === "create" ? (
              <FormActions
                saving={form.formState.isSubmitting}
                cancelTo={cancelPath}
                submitRef={saveButton}
              />
            ) : null}
          </form>
        </ResourceContent>
      </PageContent>
      <ConfirmationDialog
        open={confirmReindex}
        pending={form.formState.isSubmitting}
        title={t("form.reindexTitle")}
        description={t("form.reindexDescription")}
        pendingLabel={t("common:actions.saving")}
        onOpenChange={setConfirmReindex}
        onConfirm={() => {
          void form.handleSubmit((values) =>
            commit(values, reindexAutoSaved.current),
          )()
        }}
        onCloseAutoFocus={(event) => {
          // 确认框关闭后恢复保存按钮的键盘焦点。
          event.preventDefault()
          saveButton.current?.focus()
        }}
      />
    </div>
  )
}
