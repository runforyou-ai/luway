/** 会话共享文件区：列出、上传、保存附件、删除与下载成员和 AI 员工共用的会话文件。 */
import { FilePurpose } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import { completeFileUpload, createFileUpload, FileTransfer } from "@/api/uploads"

/** 读取会话共享文件区中的文件。 */
export const listConversationFiles = ops.listConversationFiles

/** 把上传完成的文件加入会话共享文件区。 */
export const addConversationFile = ops.addConversationFile

/** 把消息附件保存到会话共享文件区。 */
export const saveConversationAttachment = ops.saveConversationAttachment

/** 从会话共享文件区删除文件。 */
export const deleteConversationFile = ops.deleteConversationFile

/** 获取会话共享文件的下载请求。 */
export const getConversationFileDownload = ops.getConversationFileDownload

/** 上传文件并以原文件名加入会话共享文件区，同名文件已存在时由服务端追加序号。 */
export function uploadConversationFile(
  conversationID: string,
  file: globalThis.File,
  signal: AbortSignal,
  onProgress: (bytes: number) => void,
) {
  const transfer = new FileTransfer(file, () =>
    createFileUpload({
      purpose: FilePurpose.ConversationFile,
      fileName: file.name,
      contentType: file.type,
      byteSize: file.size,
    }),
  )
  return transfer.run(signal, onProgress, async (fileID) => {
    await completeFileUpload(fileID)
    return addConversationFile(conversationID, { fileId: fileID })
  })
}
