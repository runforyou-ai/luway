/** 会话共享文件区：成员与 AI 员工共用的文件列表、上传、下载与删除，桌面端侧边面板与移动端页面共用。 */
import { useRef, useState } from "react"
import { useMutation } from "@tanstack/react-query"
import { FileIcon, UploadIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  deleteConversationFile,
  getConversationFileDownload,
  listConversationFiles,
  uploadConversationFile,
  type ConversationFile,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceContent } from "@/components/resource-content"
import { ResourceRowIdentity } from "@/components/resource-row-identity"
import { ResourceTable } from "@/components/resource-table"
import { Button } from "@/components/ui/button"
import { useConversationTime, useMinuteTick } from "@/features/inbox/shared/use-conversation-time"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { formatFileSize } from "@/lib/file-size"
import { downloadURL } from "@/platform/downloads"

/** 展示会话共享文件区并提供上传、下载和删除。 */
export function ConversationFilesPanel({ conversationID }: { conversationID: string }) {
  const { t } = useTranslation(["inbox", "common"])
  const formatTime = useConversationTime()
  useMinuteTick()
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()
  const inputRef = useRef<HTMLInputElement>(null)
  const [deleting, setDeleting] = useState<ConversationFile | null>(null)
  const resource = useResource(resourceKeys.conversationFiles(conversationID), () => listConversationFiles(conversationID))

  const upload = useMutation({
    mutationFn: async (files: globalThis.File[]) => {
      // 依次上传，前一个失败时停止，已上传的文件保留在文件区。
      for (const file of files) {
        await uploadConversationFile(conversationID, file, new AbortController().signal, () => {})
      }
    },
    onSettled: () => invalidate(resourceKeys.conversationFiles(conversationID)),
  })
  const remove = useMutation({
    mutationFn: (file: ConversationFile) => deleteConversationFile(conversationID, file.id),
    onSuccess: () => invalidate(resourceKeys.conversationFiles(conversationID)),
  })

  /** 下载文件当前版本。 */
  async function download(file: ConversationFile) {
    try {
      const request = await getConversationFileDownload(conversationID, file.id)
      await downloadURL(request.url, fileName(file))
    } catch (error) {
      reportError(error, { log: "下载会话文件", fallback: t("attachmentDownloadFailed") })
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center justify-end border-b px-3 py-2.5">
        <input
          ref={inputRef}
          type="file"
          multiple
          className="hidden"
          onChange={(event) => {
            const files = Array.from(event.target.files ?? [])
            event.target.value = ""
            if (files.length > 0) {
              upload.mutate(files, {
                onError: (error) => reportError(error, { log: "上传会话文件", fallback: t("conversationFileUploadFailed") }),
              })
            }
          }}
        />
        <Button type="button" size="sm" variant="outline" disabled={upload.isPending} onClick={() => inputRef.current?.click()}>
          <UploadIcon />
          {upload.isPending ? t("conversationFileUploading") : t("conversationFileUpload")}
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-1.5">
        <ResourceContent resources={resource} errorMessage={t("conversationFilesLoadFailed")}>
          <ResourceTable
            columns={[{
              key: "file",
              header: t("conversationFiles"),
              className: "max-w-0",
              cell: (file: ConversationFile) => (
                <ResourceRowIdentity
                  icon={FileIcon}
                  name={<span title={file.path}>{file.path}</span>}
                  secondary={formatFileSize(file.byteSize)}
                  description={file.updatedByName
                    ? t("conversationFileUpdatedBy", { time: formatTime(file.updatedAt), name: file.updatedByName })
                    : t("conversationFileUpdated", { time: formatTime(file.updatedAt) })}
                />
              ),
            }]}
            rows={resource.data?.items ?? []}
            rowKey={(file) => file.id}
            empty={t("conversationFilesEmpty")}
            onRowActivate={(file) => void download(file)}
            rowActions={(file) => [
              { key: "download", label: t("attachmentDownload"), onSelect: () => void download(file) },
              { key: "delete", label: t("common:actions.delete"), destructive: true, separatorBefore: true, onSelect: () => setDeleting(file) },
            ]}
          />
        </ResourceContent>
      </div>
      <ConfirmationDialog
        open={deleting !== null}
        pending={remove.isPending}
        title={t("conversationFileDeleteTitle", { name: deleting ? fileName(deleting) : "" })}
        description={t("conversationFileDeleteDescription")}
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={() => deleting && remove.mutate(deleting, {
          onSuccess: () => setDeleting(null),
          onError: (error) => reportError(error, { log: "删除会话文件", fallback: t("conversationFileDeleteFailed") }),
        })}
      />
    </div>
  )
}

/** 返回文件路径中的文件名。 */
function fileName(file: ConversationFile) {
  return file.path.split("/").pop() ?? file.path
}
