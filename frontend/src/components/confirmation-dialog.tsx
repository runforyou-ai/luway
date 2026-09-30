/** 展示二次确认并在请求完成前保留对话框。 */
import type { ComponentProps, ReactNode } from "react"
import { useTranslation } from "react-i18next"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { buttonVariants } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/** 统一确认文案、等待状态和关闭行为；未给出 pendingLabel 时进行中显示通用的处理中文案；删除、停用等危险操作用红色主按钮，恢复、启用等用主色；confirmLabel 与 cancelLabel 仅用于两个动作互斥的岔路弹窗。 */
export function ConfirmationDialog({
  open,
  pending,
  title,
  description,
  destructive = true,
  pendingLabel,
  confirmLabel,
  cancelLabel,
  onOpenChange,
  onConfirm,
  onCloseAutoFocus,
}: {
  open: boolean
  pending: boolean
  title: string
  description: ReactNode
  destructive?: boolean
  pendingLabel?: string
  confirmLabel?: string
  cancelLabel?: string
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
  onCloseAutoFocus?: ComponentProps<typeof AlertDialogContent>["onCloseAutoFocus"]
}) {
  const { t } = useTranslation("common")
  // 移动端按钮使用触屏尺寸。
  const buttonClassName = "touch:min-h-11"
  return (
    <AlertDialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!pending) onOpenChange(nextOpen)
      }}
    >
      <AlertDialogContent onCloseAutoFocus={onCloseAutoFocus}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel className={buttonClassName} disabled={pending}>
            {cancelLabel ?? t("actions.cancel")}
          </AlertDialogCancel>
          <AlertDialogAction
            className={cn(!destructive && buttonVariants(), buttonClassName)}
            disabled={pending}
            onClick={(event) => {
              event.preventDefault()
              onConfirm()
            }}
          >
            {pending
              ? (pendingLabel ?? t("actions.processing"))
              : (confirmLabel ?? t("actions.confirm"))}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
