/** 助理记忆页签：列表、编辑弹窗与删除确认；记忆表单与删除操作供移动端复用。 */
import { useEffect, useMemo, useRef } from "react"
import { BookmarkIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  deleteAssistantMemory,
  isApiError,
  listAssistantMemories,
  updateAssistantMemory,
  type AssistantMemory,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ResourceContent } from "@/components/resource-content"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useEditingDialog } from "@/hooks/use-editing-dialog"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useDateTime } from "@/hooks/use-date-time"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 读取助理的记忆；记忆由后台提取任务写入，每次打开或回到窗口时重新读取。 */
export function useAssistantMemories(assistantId: string) {
  return useResource(
    resourceKeys.assistantMemories(assistantId),
    () => listAssistantMemories(assistantId),
    { staleTime: 0, refetchOnWindowFocus: true },
  )
}

/** 读取助理的记忆，按最近更新列出并承载编辑与删除。 */
export function AssistantMemoryPanel({ assistantId }: { assistantId: string }) {
  const { t } = useTranslation(["contacts", "common"])
  const { formatDateTime } = useDateTime()
  const memories = useAssistantMemories(assistantId)
  const invalidate = useResourceInvalidator()
  const editor = useEditingDialog<AssistantMemory>()

  const deletion = useAssistantMemoryDeletion(assistantId)

  return (
    <ResourceContent
      resources={[memories]}
      errorMessage={t("assistants.memory.loadError")}
    >
      <ResourceTable
        columns={[
          {
            key: "name",
            header: t("assistants.memory.form.name"),
            cell: (memory) => (
              <ResourceRowIdentity
                icon={BookmarkIcon}
                name={memory.name}
                description={memory.description}
              />
            ),
          },
          {
            key: "updatedAt",
            header: t("assistants.memory.updatedAtColumn"),
            cellClassName: "w-px whitespace-nowrap text-right text-muted-foreground tabular-nums",
            cell: (memory) =>
              t("assistants.memory.updatedAt", { time: formatDateTime(memory.updatedAt) }),
          },
        ]}
        rows={memories.data?.memories ?? []}
        rowKey={(memory) => memory.id}
        empty={t("assistants.memory.empty")}
        onRowActivate={(memory) => editor.open(memory)}
        rowActions={(memory) => [
          {
            key: "edit",
            label: t("common:actions.edit"),
            onSelect: () => editor.open(memory),
          },
          {
            key: "delete",
            label: t("common:actions.delete"),
            destructive: true,
            separatorBefore: true,
            onSelect: () => deletion.select(memory),
          },
        ]}
      />

      <Dialog
        open={editor.editing !== null}
        onOpenChange={(open) => !open && editor.close()}
      >
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("assistants.memory.edit")}</DialogTitle>
            <DialogDescription>
              {t("assistants.memory.editDescription")}
            </DialogDescription>
          </DialogHeader>
          {editor.editing?.item ? (
            <AssistantMemoryForm
              assistantId={assistantId}
              memory={editor.editing.item}
              onSaved={() => {
                void invalidate(resourceKeys.assistantMemories(assistantId))
                editor.finish(editor.editing)
              }}
              onCancel={editor.close}
            />
          ) : null}
        </DialogContent>
      </Dialog>

      <ConfirmationDialog
        {...deletion.dialog}
        title={t("assistants.memory.deleteTitle", {
          name: deletion.item?.name ?? "",
        })}
        description={t("assistants.memory.deleteDescription")}
        pendingLabel={t("common:actions.deleting")}
      />
    </ResourceContent>
  )
}

/** 确认后删除助理记忆，成功后刷新记忆列表并调用 onSuccess。 */
export function useAssistantMemoryDeletion(assistantId: string, onSuccess?: () => void) {
  const { t } = useTranslation("contacts")
  return useConfirmedAction<AssistantMemory>({
    action: (memory) => deleteAssistantMemory(assistantId, memory.id),
    invalidateKeys: () => [resourceKeys.assistantMemories(assistantId)],
    successMessage: () => t("assistants.memory.deleted"),
    errorMessage: () => t("assistants.memory.deleteError"),
    logLabel: "删除助理记忆",
    onSuccess,
  })
}

/** 助理记忆表单校验规则。 */
function createAssistantMemorySchema(messages: {
  nameRequired: string
  nameTooLong: string
  descriptionRequired: string
  descriptionTooLong: string
  bodyRequired: string
  bodyTooLong: string
}) {
  return z.object({
    name: z.string().trim().min(1, messages.nameRequired).max(60, messages.nameTooLong),
    description: z.string().trim().min(1, messages.descriptionRequired).max(200, messages.descriptionTooLong),
    body: z.string().trim().min(1, messages.bodyRequired).max(2000, messages.bodyTooLong),
  })
}

type AssistantMemoryFormValues = z.infer<
  ReturnType<typeof createAssistantMemorySchema>
>

/** 保存助理记忆的名称、说明与内容；未修改时跟随重新读取的记忆，移动端以整行保存按钮提交并由页头返回取消。 */
export function AssistantMemoryForm({
  assistantId,
  memory,
  onSaved,
  onCancel,
}: {
  assistantId: string
  memory: AssistantMemory
  onSaved: () => void
  onCancel?: () => void
}) {
  const mobile = resolveAppPlatform() === "mobile"
  const { t } = useTranslation(["contacts", "common"])
  const navigate = useNavigate()
  const schema = useMemo(
    () =>
      createAssistantMemorySchema({
        nameRequired: t("assistants.memory.validation.nameRequired"),
        nameTooLong: t("assistants.memory.validation.nameTooLong"),
        descriptionRequired: t("assistants.memory.validation.descriptionRequired"),
        descriptionTooLong: t("assistants.memory.validation.descriptionTooLong"),
        bodyRequired: t("assistants.memory.validation.bodyRequired"),
        bodyTooLong: t("assistants.memory.validation.bodyTooLong"),
      }),
    [t],
  )
  const form = useForm<AssistantMemoryFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      name: memory.name,
      description: memory.description,
      body: memory.body,
    },
  })
  const { isDirty } = form.formState
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    // 移动端打开时不弹出键盘。
    if (!mobile) form.setFocus("body")
    return () => {
      mounted.current = false
    }
  }, [form, mobile])
  useEffect(() => {
    // 重新读取到新内容且表单未修改时同步显示。
    if (isDirty) return
    form.reset({ name: memory.name, description: memory.description, body: memory.body })
  }, [form, isDirty, memory.name, memory.description, memory.body])

  /** 提交记忆的修改。 */
  async function submit(values: AssistantMemoryFormValues) {
    try {
      await updateAssistantMemory(assistantId, memory.id, values)
      toast.success(t("assistants.memory.saved"))
      // 表单已卸载时不再触发后续导航。
      if (mounted.current) onSaved()
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("保存助理记忆失败", { assistant_id: assistantId, memory_id: memory.id, error })
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["name", "description", "body"])
          : t("assistants.memory.saveError"),
      )
    }
  }

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <FormInputField
          name="name"
          control={form.control}
          label={t("assistants.memory.form.name")}
          required
        />
        <Controller
          name="description"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("assistants.memory.form.description")}
              </FieldLabel>
              <Input
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              />
              <FieldDescription>
                {t("assistants.memory.form.descriptionHelp")}
              </FieldDescription>
            </Field>
          )}
        />
        <Controller
          name="body"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("assistants.memory.form.body")}
              </FieldLabel>
              <Textarea
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              />
            </Field>
          )}
        />
      </FieldGroup>
      {mobile ? (
        <Button type="submit" className="min-h-11 w-full" disabled={form.formState.isSubmitting}>
          {form.formState.isSubmitting ? t("common:actions.saving") : t("common:actions.save")}
        </Button>
      ) : (
        <FormActions saving={form.formState.isSubmitting} onCancel={onCancel} />
      )}
    </form>
  )
}
