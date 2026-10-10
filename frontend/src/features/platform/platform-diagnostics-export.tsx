/** 导出诊断信息的按钮：生成诊断信息并保存为以导出时间命名的 JSON 文件。 */
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { getPlatformDiagnostics } from "@/api"
import { Button } from "@/components/ui/button"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { saveTextFile } from "@/platform/downloads"

/** 点击后生成诊断信息并保存，原生端取消保存时不提示。 */
export function DiagnosticsExportButton() {
  const { t } = useTranslation("platform")
  const reportError = useRequestErrorReporter()
  const diagnosticsExport = useMutation({
    mutationFn: async () => {
      const diagnostics = await getPlatformDiagnostics()
      // 文件名使用本地时间，格式为 diagnostics-YYYYMMDD-HHmmss.json。
      const now = new Date()
      const pad = (value: number) => String(value).padStart(2, "0")
      const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
      return saveTextFile(`diagnostics-${stamp}.json`, JSON.stringify(diagnostics, null, 2), "application/json")
    },
    onSuccess: (saved) => {
      if (saved) toast.success(t("runtime.diagnosticsExported"))
    },
    onError: (error) => reportError(error, { log: "导出诊断信息", fallback: t("runtime.exportDiagnosticsError") }),
  })
  const exporting = diagnosticsExport.isPending

  return (
    <Button variant="outline" size="sm" disabled={exporting} onClick={() => diagnosticsExport.mutate()}>
      {exporting ? t("runtime.exportingDiagnostics") : t("runtime.exportDiagnostics")}
    </Button>
  )
}
