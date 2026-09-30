/** 客服处理周期小结：读取客户周期小结、展示小结正文与标注，并由客服修改。 */
import { useEffect, useMemo } from "react"
import { PencilIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  getServiceSummaries,
  isApiError,
  listServiceCategories,
  ServiceSummaryStatus,
  updateServiceSessionSummary,
  type ServiceSessionSummary,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import { useEditingDialog } from "@/hooks/use-editing-dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 读取客户会话的交接摘要与同一客户已关闭周期的小结，随会话内容变化刷新。 */
export function useServiceSummaries(conversationID: string) {
  return useResource(resourceKeys.serviceSummaries(conversationID), () =>
    getServiceSummaries(conversationID),
  )
}

/** 展示小结正文或生成状态，以及咨询分类、是否解决与修改人。 */
export function ServiceSessionSummaryText({
  summary,
  meta = [],
}: {
  summary: ServiceSessionSummary
  meta?: string[]
}) {
  const { t } = useTranslation("inbox")
  // 没有正文时按生成状态说明。
  const statusText =
    summary.status === ServiceSummaryStatus.ServiceSummaryPending
      ? t("summaryPending")
      : summary.status === ServiceSummaryStatus.ServiceSummaryFailed
        ? t("summaryFailed")
        : summary.status === ServiceSummaryStatus.ServiceSummaryNoRequest
          ? t("summaryNoRequest")
          : t("summaryEmpty")
  const labels = [
    ...meta,
    summary.categoryName ?? "",
    summary.resolved === null ? "" : t(summary.resolved ? "summaryResolved" : "summaryUnresolved"),
    summary.editedBy ? t("summaryEditedBy", { name: summary.editedBy }) : "",
  ].filter(Boolean)
  return (
    <div className="min-w-0 space-y-1">
      <p
        className={
          summary.summary
            ? "text-sm leading-5 whitespace-pre-wrap break-words text-foreground"
            : "text-sm leading-5 text-muted-foreground"
        }
      >
        {summary.summary ?? statusText}
      </p>
      {labels.length > 0 ? (
        <p className="text-xs text-muted-foreground">{labels.join(" · ")}</p>
      ) : null}
    </div>
  )
}

/** 小结修改入口：图标按钮打开修改对话框。 */
export function ServiceSessionSummaryEditButton({
  conversationID,
  summary,
  className,
}: {
  conversationID: string
  summary: ServiceSessionSummary
  className?: string
}) {
  const { t } = useTranslation("inbox")
  const editor = useEditingDialog<never>()
  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={className ?? "size-7 text-muted-foreground"}
        aria-label={t("summaryEdit")}
        title={t("summaryEdit")}
        onClick={() => editor.open()}
      >
        <PencilIcon />
      </Button>
      <Dialog open={editor.editing !== null} onOpenChange={(next) => !next && editor.close()}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("summaryEditTitle")}</DialogTitle>
            <DialogDescription>{t("summaryEditDescription")}</DialogDescription>
          </DialogHeader>
          {editor.editing !== null ? (
            <ServiceSessionSummaryForm
              key={editor.editing.session}
              conversationID={conversationID}
              summary={summary}
              onSaved={() => editor.finish(editor.editing)}
              onCancel={editor.close}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  )
}

/** 创建小结表单校验规则。 */
function createSummarySchema(summaryTooLong: string) {
  return z.object({
    summary: z.string().trim().max(2000, summaryTooLong),
    categoryId: z.string(),
    resolved: z.enum(["", "true", "false"]),
  })
}

type ServiceSessionSummaryFormValues = z.infer<ReturnType<typeof createSummarySchema>>

/** 修改小结正文、咨询分类与是否解决，保存后刷新客户周期小结。 */
function ServiceSessionSummaryForm({
  conversationID,
  summary,
  onSaved,
  onCancel,
}: {
  conversationID: string
  summary: ServiceSessionSummary
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation(["inbox", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const categories = useResource(resourceKeys.serviceCategories(), () => listServiceCategories())
  const schema = useMemo(() => createSummarySchema(t("summaryTooLong")), [t])
  const form = useForm<ServiceSessionSummaryFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      summary: summary.summary ?? "",
      categoryId: summary.categoryId ?? "",
      resolved: summary.resolved === null ? "" : summary.resolved ? "true" : "false",
    },
  })
  useEffect(() => {
    form.setFocus("summary")
  }, [form])
  // 当前分类已归档时仍作为选项保留。
  const categoryOptions = categories.data?.categories.map((category) => ({ id: category.id, name: category.name })) ?? []
  if (summary.categoryId && summary.categoryName && !categoryOptions.some((category) => category.id === summary.categoryId)) {
    categoryOptions.unshift({ id: summary.categoryId, name: summary.categoryName })
  }

  /** 保存客服修改的小结。 */
  async function submit(values: ServiceSessionSummaryFormValues) {
    try {
      await updateServiceSessionSummary(summary.serviceSessionId, {
        summary: values.summary,
        categoryId: values.categoryId || null,
        resolved: values.resolved === "" ? null : values.resolved === "true",
      })
      toast.success(t("summarySaved"))
      void invalidate(resourceKeys.serviceSummaries(conversationID))
      onSaved()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("保存会话小结失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["summary", "categoryId", "resolved"])
          : t("summarySaveError"),
      )
    }
  }

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <Controller
          name="summary"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="service-summary">{t("summaryText")}</FieldLabel>
              <Textarea {...field} id="service-summary" aria-invalid={fieldState.invalid} />
            </Field>
          )}
        />
        <Controller
          name="categoryId"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="service-summary-category">{t("summaryCategory")}</FieldLabel>
              <NativeSelect {...field} id="service-summary-category">
                <option value="">{t("summaryNotLabeled")}</option>
                {categoryOptions.map((category) => (
                  <option key={category.id} value={category.id}>
                    {category.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        />
        <Controller
          name="resolved"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="service-summary-resolved">{t("summaryResolution")}</FieldLabel>
              <NativeSelect {...field} id="service-summary-resolved">
                <option value="">{t("summaryNotLabeled")}</option>
                <option value="true">{t("summaryResolved")}</option>
                <option value="false">{t("summaryUnresolved")}</option>
              </NativeSelect>
            </Field>
          )}
        />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
    </form>
  )
}
