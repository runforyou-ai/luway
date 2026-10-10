/** 工作区电脑弹窗：填写名称添加电脑，添加或重置凭据后展示只返回一次的执行器连接信息与执行器下载入口。 */
import { useMutation } from "@tanstack/react-query"
import { DownloadIcon, LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { z } from "zod"

import { createWorkspaceComputer, type ComputerRegistration } from "@/api"
import { serverURL } from "@/api/client"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { clientDownloadPath } from "@/lib/client-download"
import { zodResolver } from "@/lib/zod-resolver"
import { openResolvedExternalURL } from "@/platform/system"

/** 添加工作区电脑，registration 非空时直接展示该次添加或重置凭据得到的连接信息。 */
export function WorkspaceComputerDialog({
  open,
  registration,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  registration: ComputerRegistration | null
  onOpenChange: (open: boolean) => void
  onCreated: (registration: ComputerRegistration) => void
}) {
  const { t, i18n } = useTranslation("integrations")

  /** 在系统浏览器中打开当前服务器下载页的执行器一节，失败时提示。 */
  function openExecutorDownload() {
    openResolvedExternalURL(async () => `${serverURL()}${clientDownloadPath(i18n.language)}#executor`).catch((error: unknown) => {
      console.warn("打开执行器下载页失败", { error })
      toast.error(t("computer.credential.downloadError"))
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {registration ? t("computer.credential.title", { name: registration.computer.name }) : t("computer.create.title")}
          </DialogTitle>
          <DialogDescription>
            {registration ? t("computer.credential.description") : t("computer.create.description")}
          </DialogDescription>
        </DialogHeader>
        {registration ? (
          <div className="space-y-9">
            <ComputerConnection credential={registration.credential} />
            <div className="flex items-center justify-between gap-2">
              <Button type="button" variant="ghost" onClick={openExecutorDownload}>
                <DownloadIcon />
                {t("computer.credential.download")}
              </Button>
              <Button type="button" onClick={() => onOpenChange(false)}>
                {t("computer.credential.done")}
              </Button>
            </div>
          </div>
        ) : open ? (
          <WorkspaceComputerForm onCancel={() => onOpenChange(false)} onCreated={onCreated} />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

/** 展示执行器连接服务端使用的环境变量并提供复制。 */
function ComputerConnection({ credential }: { credential: string }) {
  const { t } = useTranslation(["integrations", "common"])
  const { copied, copy } = useCopyFeedback<"environment">()
  const environment = `EXECUTOR_SERVER_URL=${serverURL()}\nEXECUTOR_CREDENTIAL=${credential}`

  /** 复制环境变量，失败时提示手动复制。 */
  async function copyEnvironment() {
    if (!(await copy(environment, "environment"))) toast.error(t("computer.credential.copyError"))
  }

  return (
    <Field>
      <FieldLabel>{t("computer.credential.environment")}</FieldLabel>
      <div className="flex items-start gap-2 rounded-md border bg-muted/30 px-3 py-2">
        <code className="min-w-0 flex-1 py-1.5 font-mono text-xs break-all whitespace-pre-wrap">{environment}</code>
        <Button type="button" variant="outline" size="sm" className="shrink-0" onClick={() => void copyEnvironment()}>
          {copied === "environment" ? t("common:actions.copied") : t("common:actions.copy")}
        </Button>
      </div>
      <FieldDescription>{t("computer.credential.environmentHelp")}</FieldDescription>
    </Field>
  )
}

/** 工作区电脑名称表单校验规则。 */
const workspaceComputerSchema = z.object({
  name: z.string().trim().min(1).max(100),
})

/** 工作区电脑名称表单。 */
function WorkspaceComputerForm({
  onCancel,
  onCreated,
}: {
  onCancel: () => void
  onCreated: (registration: ComputerRegistration) => void
}) {
  const { t } = useTranslation(["integrations", "common"])
  const reportError = useRequestErrorReporter()
  const creation = useMutation({ mutationFn: (name: string) => createWorkspaceComputer(name) })
  const form = useForm<z.infer<typeof workspaceComputerSchema>>({
    resolver: zodResolver(workspaceComputerSchema),
    shouldUseNativeValidation: true,
    defaultValues: { name: "" },
  })

  /** 添加工作区电脑；表单随弹窗关闭卸载，卸载后返回的凭据直接丢弃。 */
  function submit(values: z.infer<typeof workspaceComputerSchema>) {
    creation.mutate(values.name, {
      onSuccess: onCreated,
      onError: (error) => reportError(error, { log: "添加工作区电脑", fields: ["name"] }),
    })
  }

  const isSubmitting = creation.isPending

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup className="gap-5">
        <FormInputField name="name" control={form.control} label={t("computer.form.name")} autoFocus />
      </FieldGroup>
      <div className="flex items-center justify-end gap-2">
        <Button type="button" variant="outline" disabled={isSubmitting} onClick={onCancel}>
          {t("common:actions.cancel")}
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {isSubmitting ? t("computer.create.submitting") : t("computer.create.submit")}
        </Button>
      </div>
    </form>
  )
}
