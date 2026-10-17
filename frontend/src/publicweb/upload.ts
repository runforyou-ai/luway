/** 网站访客附件上传：独立打包为 IIFE，与主应用共用 Uppy 的 S3 传输实现，不含会话隔离。 */
import AwsS3 from "@uppy/aws-s3"
import Uppy from "@uppy/core"
import type { FileUploadRequest } from "@/api/generated/contract"

export function upload(file: File, request: FileUploadRequest, onProgress: (bytes: number) => void) {
  const uppy = new Uppy({ restrictions: { maxNumberOfFiles: 1 } })
  uppy.use(AwsS3, {
    shouldUseMultipart: false,
    generateObjectKey: () => file.name,
    signRequest: async () => ({ url: request.url, headers: request.headers }),
  })
  uppy.addFile({ name: file.name, type: file.type, data: file })
  let cancel = () => {}
  const done = new Promise<void>((resolve, reject) => {
    cancel = () => { uppy.cancelAll(); reject(new DOMException("Upload cancelled", "AbortError")) }
    uppy.on("upload-progress", (_file, progress) => onProgress(progress.bytesUploaded))
    uppy.on("upload-success", () => resolve())
    uppy.on("upload-error", (_file, error) => reject(error))
    void uppy.upload().catch(reject)
  }).finally(() => uppy.destroy())
  return { abort: () => cancel(), done }
}
