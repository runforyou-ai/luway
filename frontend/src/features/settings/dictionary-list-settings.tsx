/** 设置页字典式列表：标题行与新建按钮、名称列表、编辑与删除行操作、编辑弹窗和删除确认。 */
import type { ReactNode } from "react"
import type { QueryKey } from "@tanstack/react-query"
import { PlusIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useEditingDialog } from "@/hooks/use-editing-dialog"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 列出字典条目并承载新增、编辑与删除；保存或删除成功后失效 invalidateKeys，renderForm 渲染弹窗内的新增或编辑表单。 */
export function DictionaryListSettings<T extends { id: string; name: string }>({
  title,
  description,
  createLabel,
  editLabel,
  formDescription,
  dialogClassName,
  nameHeader,
  rows,
  empty,
  renderRow,
  renderForm,
  invalidateKeys,
  deletion,
}: {
  title?: string
  description: string
  createLabel: string
  editLabel: string
  formDescription?: string
  dialogClassName: string
  nameHeader: string
  rows: readonly T[]
  empty: string
  renderRow: (item: T) => ReactNode
  renderForm: (item: T | undefined, actions: { onSaved: () => void; onCancel: () => void }) => ReactNode
  invalidateKeys: QueryKey[]
  deletion: {
    action: (item: T) => Promise<unknown>
    title: (name: string) => string
    description: string
    success: string
    error: string
    logLabel: string
  }
}) {
  const { t } = useTranslation("common")
  const invalidate = useResourceInvalidator()
  const editor = useEditingDialog<T>()
  const remove = useConfirmedAction<T>({
    action: deletion.action,
    invalidateKeys: () => invalidateKeys,
    successMessage: () => deletion.success,
    errorMessage: () => deletion.error,
    logLabel: deletion.logLabel,
  })

  return (
    <>
      <section className="space-y-3">
        <div className="flex items-center justify-between gap-3">
          {title ? (
            <div className="min-w-0">
              <h3 className="text-sm font-medium">{title}</h3>
              <p className="text-sm text-muted-foreground">{description}</p>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">{description}</p>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            className="shrink-0"
            aria-label={createLabel}
            title={createLabel}
            onClick={() => editor.open()}
          >
            <PlusIcon />
          </Button>
        </div>
        <ResourceTable
          columns={[{ key: "name", header: nameHeader, cell: renderRow }]}
          rows={rows}
          rowKey={(item) => item.id}
          empty={empty}
          onRowActivate={(item) => editor.open(item)}
          rowActions={(item) => [
            {
              key: "edit",
              label: t("actions.edit"),
              onSelect: () => editor.open(item),
            },
            {
              key: "delete",
              label: t("actions.delete"),
              destructive: true,
              separatorBefore: true,
              onSelect: () => remove.select(item),
            },
          ]}
        />
      </section>

      <Dialog
        open={editor.editing !== null}
        onOpenChange={(open) => !open && editor.close()}
      >
        <DialogContent className={dialogClassName}>
          <DialogHeader>
            <DialogTitle>{editor.editing?.item ? editLabel : createLabel}</DialogTitle>
            {formDescription ? <DialogDescription>{formDescription}</DialogDescription> : null}
          </DialogHeader>
          {editor.editing !== null
            ? renderForm(editor.editing.item, {
                onSaved: () => {
                  for (const key of invalidateKeys) void invalidate(key)
                  editor.finish(editor.editing)
                },
                onCancel: editor.close,
              })
            : null}
        </DialogContent>
      </Dialog>

      <ConfirmationDialog
        {...remove.dialog}
        title={deletion.title(remove.item?.name ?? "")}
        description={deletion.description}
        pendingLabel={t("actions.deleting")}
      />
    </>
  )
}
