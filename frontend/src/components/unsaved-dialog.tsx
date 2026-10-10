/** 内含表单的弹窗，有未保存内容时按 Esc、点遮罩或关闭按钮先确认放弃修改。 */
import { useMemo, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { Dialog } from "@/components/ui/dialog"
import {
  UnsavedChangesContext,
  useUnsavedChangesContext,
  type UnsavedForm,
} from "@/contexts/unsaved-changes-context"

/** 收集弹窗内登记的表单并同步登记到页面守卫，关闭弹窗前确认放弃有改动的表单。 */
export function UnsavedDialog({
  open,
  onOpenChange,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  children: ReactNode
}) {
  const { t } = useTranslation("common")
  const outer = useUnsavedChangesContext()
  const forms = useRef(new Map<symbol, UnsavedForm>())
  const [confirming, setConfirming] = useState(false)
  // 弹窗被外部关闭时撤销进行中的确认。
  if (!open && confirming) setConfirming(false)
  const context = useMemo(
    () => ({
      register: (id: symbol, form: UnsavedForm) => {
        forms.current.set(id, form)
        const unregister = outer?.register(id, form)
        return () => {
          forms.current.delete(id)
          unregister?.()
        }
      },
      confirmDiscard: outer?.confirmDiscard ?? (() => Promise.resolve(true)),
    }),
    [outer],
  )

  /** 关闭时有改动的表单先进入确认，其余情况直接透传。 */
  function handleOpenChange(next: boolean) {
    if (!next && [...forms.current.values()].some((form) => form.dirty.current)) {
      setConfirming(true)
      return
    }
    onOpenChange(next)
  }

  return (
    <>
      <Dialog open={open} onOpenChange={handleOpenChange}>
        <UnsavedChangesContext.Provider value={context}>
          {children}
        </UnsavedChangesContext.Provider>
      </Dialog>
      <ConfirmationDialog
        open={open && confirming}
        pending={false}
        title={t("unsavedChanges.title")}
        description={t("unsavedChanges.description")}
        confirmLabel={t("unsavedChanges.discard")}
        cancelLabel={t("unsavedChanges.keepEditing")}
        onOpenChange={(next) => {
          if (!next) setConfirming(false)
        }}
        onConfirm={() => {
          // 确认放弃后标记弹窗内的改动，卸载时跳过等待保存的内容。
          for (const form of forms.current.values()) {
            if (form.dirty.current) form.discarded.current = true
          }
          setConfirming(false)
          onOpenChange(false)
        }}
      />
    </>
  )
}
