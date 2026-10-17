/** 生成包含运行摘要与本机日志的诊断包，支持下载与主动上传。 */
import { useMutation } from "@tanstack/react-query"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { exportDiagnosticBundle, uploadDiagnosticBundle } from "@/api"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { saveBinaryFile } from "@/platform/downloads"

/** 下载诊断包，或确认后主动上传到 control。 */
export function DiagnosticsExportButton() {
  const { t } = useTranslation(["platform", "common"])
  const reportError = useRequestErrorReporter()
  const [uploading, setUploading] = useState(false)
  const diagnosticsExport = useMutation({
    mutationFn: async () => {
      const bundle = await exportDiagnosticBundle()
      return saveBinaryFile(bundle.name, bundle.content, "application/zip")
    },
    onSuccess: (saved) => { if (saved) toast.success(t("runtime.diagnosticsExported")) },
    onError: (error) => reportError(error, { log: "导出诊断包", fallback: t("runtime.exportDiagnosticsError") }),
  })
  const diagnosticsUpload = useMutation({
    mutationFn: () => uploadDiagnosticBundle({}),
    onError: (error) => reportError(error, { log: "上传诊断包", fallback: t("runtime.uploadDiagnosticsError") }),
  })

  return (
    <>
      <Button variant="outline" size="sm" disabled={diagnosticsExport.isPending} onClick={() => diagnosticsExport.mutate()}>
        {diagnosticsExport.isPending ? t("runtime.exportingDiagnostics") : t("runtime.exportDiagnostics")}
      </Button>
      <Button variant="outline" size="sm" onClick={() => setUploading(true)}>{t("runtime.uploadDiagnostics")}</Button>
      <Dialog open={uploading} onOpenChange={(open) => !diagnosticsUpload.isPending && setUploading(open)}>
        <DialogContent closeDisabled={diagnosticsUpload.isPending}>
          <DialogHeader>
            <DialogTitle>{t("runtime.uploadDiagnostics")}</DialogTitle>
            <DialogDescription>{t("runtime.uploadDiagnosticsHelp")}</DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button variant="outline" disabled={diagnosticsUpload.isPending} onClick={() => setUploading(false)}>{t("common:actions.cancel")}</Button>
            <Button disabled={diagnosticsUpload.isPending} onClick={() => diagnosticsUpload.mutate(undefined, { onSuccess: () => {
              setUploading(false)
              toast.success(t("runtime.diagnosticsUploaded"))
            } })}>{t("common:actions.confirm")}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
