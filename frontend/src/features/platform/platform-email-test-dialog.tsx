/** 在模态框内填写收件邮箱并发送测试邮件。 */
import { useMemo, type RefObject } from "react"
import { useMutation } from "@tanstack/react-query"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { z } from "zod"

import { testPlatformEmail } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FieldGroup } from "@/components/ui/field"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { zodResolver } from "@/lib/zod-resolver"

/** 管理测试邮件表单与发送状态，成功后关闭并恢复触发按钮焦点。 */
export function EmailTestDialog({ open, onOpenChange, triggerRef }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  triggerRef: RefObject<HTMLButtonElement | null>
}) {
  const { t } = useTranslation(["platform", "common"])
  const reportError = useRequestErrorReporter()
  const mutation = useMutation({ mutationFn: (input: { to: string }) => testPlatformEmail(input) })
  const schema = useMemo(() => z.object({ to: z.email(t("common:validation.emailInvalid")) }), [t])
  const form = useForm<{ to: string }>({ resolver: zodResolver(schema), shouldUseNativeValidation: true, defaultValues: { to: "" } })

  /** 发送完成或取消时清空收件邮箱并关闭模态框。 */
  function close() {
    form.reset()
    onOpenChange(false)
  }

  return <Dialog open={open} onOpenChange={next => { if (!next && !mutation.isPending) close() }}>
    <DialogContent closeDisabled={mutation.isPending} onCloseAutoFocus={event => {
      event.preventDefault()
      triggerRef.current?.focus()
    }}>
      <DialogHeader>
        <DialogTitle>{t("deployment.email.test")}</DialogTitle>
        <DialogDescription>{t("deployment.email.testDescription")}</DialogDescription>
      </DialogHeader>
      <form className="space-y-9" onSubmit={form.handleSubmit(values => {
        if (mutation.isPending) return
        mutation.mutate(values, {
          onSuccess: () => { close(); toast.success(t("deployment.email.testSent")) },
          onError: error => reportError(error, { log: "发送测试邮件" }),
        })
      })} noValidate>
        <FieldGroup>
          <FormInputField name="to" control={form.control} label={t("deployment.email.testTo")} type="email" disabled={mutation.isPending} />
        </FieldGroup>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" disabled={mutation.isPending} onClick={close}>{t("common:actions.cancel")}</Button>
          <Button type="submit" disabled={mutation.isPending}>{mutation.isPending ? t("deployment.email.testing") : t("deployment.email.test")}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
}
