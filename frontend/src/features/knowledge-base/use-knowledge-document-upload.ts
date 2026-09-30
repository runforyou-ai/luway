/** 管理知识文档上传批次、失败重试与临时文件释放。 */
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import {
  FilePurpose,
  KnowledgeDocumentFormat,
  FileTransfer,
  createFileUpload,
  completeFileUpload,
  createKnowledgeDocuments,
  cancelFileUpload,
  isApiError,
} from "@/api"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"

type UploadItem = {
  file: File
  transfer: FileTransfer
  controller: AbortController
  stage: "waiting" | "uploading" | "saving" | "failed" | "saved"
  bytes: number
}
export const knowledgeDocumentFormats = Object.values(KnowledgeDocumentFormat).filter((value) => value.startsWith("."))
const knowledgeDocumentMaxByteSize = 20 * 1024 * 1024

/** 管理批次上传、逐项重试及离开页面后的临时文件清理。 */
export function useKnowledgeDocumentUpload({ baseId }: { baseId: string }) {
  const { t } = useTranslation("knowledgeBase")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const items = useRef<UploadItem[]>([])
  const mounted = useMountedRef()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [, render] = useState(0)
  useEffect(() => {
    return () => {
      for (const item of items.current) {
        // 等待已提交保存命令的事务结果。
        if (item.stage === "saving" || item.stage === "saved") continue
        item.controller.abort()
        if (item.transfer.upload) void cancelFileUpload(item.transfer.upload.file.id).catch(() => {})
      }
    }
  }, [])

  /** 逐项上传并保存，重试使用原文件编号保持幂等。 */
  async function run() {
    setBusy(true)
    let saved = 0
    for (const item of items.current) {
      if (!mounted.current || item.stage === "saved") continue
      item.stage = "uploading"
      render((value) => value + 1)
      try {
        await item.transfer.run(
          item.controller.signal,
          (bytes) => {
            item.bytes = bytes
            if (mounted.current) render((value) => value + 1)
          },
          async (fileId) => {
            await completeFileUpload(fileId)
            item.controller.signal.throwIfAborted()
            item.stage = "saving"
            if (mounted.current) render((value) => value + 1)
            await createKnowledgeDocuments(baseId, { fileIds: [fileId] })
          },
        )
        item.stage = "saved"
        saved++
      } catch (error) {
        item.stage = "failed"
        if (mounted.current && recoverSession(error, navigate)) break
        if (mounted.current && !item.controller.signal.aborted) {
          console.warn("知识文档上传失败", { error })
          toast.error(isApiError(error) ? apiErrorMessage(error) : t("documents.operationFailed"))
        }
      }
      if (mounted.current) render((value) => value + 1)
    }
    if (saved) await invalidate(resourceKeys.knowledgeDocuments(baseId))
    if (!mounted.current) return
    setBusy(false)
    // 整批保存成功后收起对话框，无未保存的临时文件需要清理。
    if (items.current.every((item) => item.stage === "saved")) {
      toast.success(t("documents.upload.success", { count: items.current.length }))
      setOpen(false)
    }
  }

  /** 校验整个选择批次并立即开始上传。 */
  function select(files: File[]) {
    if (!open || items.current.length || !files.length) return
    if (files.length > 10) {
      toast.error(t("documents.upload.tooMany"))
      return
    }
    if (
      files.some(
        (file) =>
          !knowledgeDocumentFormats.includes(
            file.name.slice(file.name.lastIndexOf(".")).toLowerCase() as KnowledgeDocumentFormat,
          ),
      )
    ) {
      toast.error(t("documents.upload.unsupported"))
      return
    }
    if (files.some((file) => file.size > knowledgeDocumentMaxByteSize)) {
      toast.error(t("documents.upload.tooLarge"))
      return
    }
    items.current = files.map((file) => ({
      file,
      bytes: 0,
      stage: "waiting",
      controller: new AbortController(),
      transfer: new FileTransfer(file, () =>
        createFileUpload({
          purpose: FilePurpose.FilePurposeKnowledgeDocument,
          fileName: file.name,
          contentType: file.type,
          byteSize: file.size,
        }),
      ),
    }))
    void run()
  }

  /** 关闭批次时清理未保存的临时文件。 */
  function close() {
    if (busy) return
    for (const item of items.current) {
      if (item.stage !== "saved" && item.transfer.upload)
        void cancelFileUpload(item.transfer.upload.file.id).catch(() => {})
    }
    setOpen(false)
  }

  return {
    open,
    busy,
    items: items.current,
    run,
    select,
    close,
    // 每次打开模态框建立新的上传批次。
    show: () => {
      items.current = []
      setOpen(true)
    },
  }
}
