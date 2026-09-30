/** 导入网页作为知识文档的表单弹窗。 */
import { useEffect, useId, type RefObject } from "react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import { createKnowledgeWebDocument, isApiError } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { FieldGroup } from "@/components/ui/field"
import { resourceKeys } from "@/hooks/resource-keys"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"
import { knowledgeDocumentTitleMaxLength } from "./knowledge-base-schema"

/** 收集页面地址和名称后创建网页文档。 */
export function KnowledgeWebImportDialog({
  baseId,
  open,
  triggerRef,
  onClose,
}: {
  baseId: string
  open: boolean
  triggerRef: RefObject<HTMLButtonElement | null>
  onClose: () => void
}) {
  const { t } = useTranslation(["knowledgeBase", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const id = useId()
  const mounted = useMountedRef()
  const form = useForm<{ title: string; sourceUrl: string }>({
    resolver: zodResolver(
      z.object({
        title: z
          .string()
          .trim()
          .min(1, t("documents.titleRequired"))
          .max(knowledgeDocumentTitleMaxLength, t("documents.titleTooLong")),
        sourceUrl: z
          .string()
          .trim()
          .url(t("documents.urlInvalid"))
          .refine(
            (value) => /^https?:\/\//i.test(value),
            t("documents.urlInvalid"),
          ),
      }),
    ),
    shouldUseNativeValidation: true,
    defaultValues: { title: "", sourceUrl: "" },
  })

  // 每次打开都从空白开始，不保留上一次填写的地址和名称。
  useEffect(() => {
    if (open) form.reset({ title: "", sourceUrl: "" })
  }, [open, form])

  /** 提交导入并失效该知识库的文档列表缓存。 */
  async function save(values: { title: string; sourceUrl: string }) {
    try {
      await createKnowledgeWebDocument(baseId, values)
      await invalidate(resourceKeys.knowledgeDocuments(baseId))
      if (!mounted.current) return
      toast.success(t("documents.importSuccess"))
      onClose()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      toast.error(
        isApiError(error) ? apiErrorMessage(error) : t("documents.importError"),
      )
    }
  }

  const disabled = form.formState.isSubmitting
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!value && !disabled) onClose()
      }}
    >
      <DialogContent
        className="sm:max-w-lg"
        closeDisabled={disabled}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          triggerRef.current?.focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>{t("documents.create.import")}</DialogTitle>
        </DialogHeader>
        <form className="space-y-9" onSubmit={form.handleSubmit(save)} noValidate>
          <FieldGroup>
            <FormInputField
              control={form.control}
              name="sourceUrl"
              id={`${id}-url`}
              type="url"
              label={t("documents.sourceUrl")}
              required
              disabled={disabled}
            />
            <FormInputField
              control={form.control}
              name="title"
              id={`${id}-title`}
              label={t("documents.name")}
              required
              disabled={disabled}
            />
          </FieldGroup>
          <div className="flex items-center justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={disabled}
              onClick={onClose}
            >
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" disabled={disabled}>
              {t(
                disabled ? "common:actions.saving" : "documents.create.import",
              )}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
