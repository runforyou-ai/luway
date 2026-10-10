/** 展示表单的保存、补充操作和取消入口。 */
import type { ReactNode, Ref } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link } from "react-router"

import { Button } from "@/components/ui/button"

/** 统一表单提交状态，允许在取消之前插入业务操作；自动保存的表单只保留业务操作。取消可以是返回路由，也可以是关闭弹窗等回调。 */
export function FormActions({
  saving,
  disabled = false,
  cancelTo,
  onCancel,
  submit = true,
  submitRef,
  children,
}: {
  saving: boolean
  disabled?: boolean
  cancelTo?: string
  onCancel?: () => void
  submit?: boolean
  submitRef?: Ref<HTMLButtonElement>
  children?: ReactNode
}) {
  const { t } = useTranslation("common")
  if (!submit) {
    return children ? (
      <div className="flex items-center justify-end gap-2">{children}</div>
    ) : null
  }
  // 操作靠右排列：业务操作、取消、保存，主操作固定在最右。
  return (
    <div className="flex items-center justify-end gap-2">
      {children}
      {/* 保存中停用取消入口，离开页面后请求仍会完成。 */}
      {cancelTo !== undefined && saving ? (
        <Button type="button" variant="outline" disabled>
          {t("actions.cancel")}
        </Button>
      ) : cancelTo !== undefined ? (
        <Button type="button" variant="outline" asChild>
          <Link to={cancelTo}>{t("actions.cancel")}</Link>
        </Button>
      ) : onCancel ? (
        <Button
          type="button"
          variant="outline"
          disabled={saving}
          onClick={onCancel}
        >
          {t("actions.cancel")}
        </Button>
      ) : null}
      <Button ref={submitRef} type="submit" disabled={saving || disabled}>
        {saving ? <LoaderCircleIcon className="animate-spin" /> : null}
        {saving ? t("actions.saving") : t("actions.save")}
      </Button>
    </div>
  )
}
