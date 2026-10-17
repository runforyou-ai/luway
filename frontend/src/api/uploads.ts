/** 通过 Uppy 直传 S3，业务完成操作仍由各上传入口决定。 */
import Uppy from "@uppy/core"
import AwsS3 from "@uppy/aws-s3"
import type { FilePurpose, FileUpload } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import { currentSessionGeneration, requestWorkspace, subscribeSessionGeneration } from "@/api/session-scope"

export const cancelFileUpload = ops.cancelFileUpload
export const createFileUpload = ops.createFileUpload
export const completeFileUpload = ops.completeFileUpload

/** 上传一个表单中的临时文件。 */
export async function uploadFile(file: globalThis.File, purpose: FilePurpose) {
  const transfer = new FileUploadTask(file, () =>
    createFileUpload({
      purpose,
      fileName: file.name,
      contentType: file.type,
      byteSize: file.size,
    }),
  )
  return transfer
    .run(new AbortController().signal, () => {}, completeFileUpload)
    .catch(async (error: unknown) => {
      await transfer.cancel()
      throw error
    })
    .finally(() => transfer.dispose())
}

/** 保存业务文件编号和 Uppy 状态，失败重试继续使用同一个上传会话。 */
export class FileUploadTask {
  upload: FileUpload | null = null
  bytes = 0
  private uppy: Uppy | null = null
  private uppyFileID = ""
  private started = false
  private contentUploaded = false
  private readonly generation = currentSessionGeneration()
  private readonly workspace = requestWorkspace()

  constructor(private readonly file: globalThis.File, private readonly prepare: () => Promise<FileUpload>) {}

  /** 阻止旧会话在新的部署、账号或工作区中继续签名和提交。 */
  private assertSession() {
    if (this.generation !== currentSessionGeneration() || this.workspace !== requestWorkspace()) {
      throw new DOMException("Upload session changed", "AbortError")
    }
  }

  /** 丢弃任务时释放 Uppy 及其浏览器事件。 */
  dispose() {
    this.uppy?.destroy()
    this.uppy = null
  }

  /** 放弃一次性任务时清理服务端临时文件；旧会话由服务端到期清理。 */
  async cancel() {
    this.dispose()
    if (!this.upload || this.generation !== currentSessionGeneration() || this.workspace !== requestWorkspace()) return
    try {
      await cancelFileUpload(this.upload.file.id)
    } catch (error) {
      console.warn("上传临时文件释放失败", error)
    }
  }

  /** Uppy 负责传输与合并；业务确认失败时只重试确认。 */
  async run<T>(signal: AbortSignal, onProgress: (bytes: number) => void, complete: (fileID: string) => Promise<T>): Promise<T> {
    signal.throwIfAborted()
    this.assertSession()
    this.upload ??= await this.prepare()
    signal.throwIfAborted()
    this.assertSession()
    const upload = this.upload
    if (this.started && !this.contentUploaded) {
      // 合并响应可能丢失；先核验已存在的对象，避免重试已被 S3 关闭的分片会话。
      try {
        await completeFileUpload(upload.file.id)
        this.contentUploaded = true
        this.dispose()
      } catch {
        signal.throwIfAborted()
        this.assertSession()
      }
    }
    if (!this.contentUploaded) {
      if (!this.uppy) {
        this.uppy = new Uppy({ autoProceed: false, restrictions: { maxNumberOfFiles: 1 } })
        this.uppy.use(AwsS3, {
          limit: 1,
          shouldUseMultipart: upload.partSize > 0,
          getChunkSize: () => upload.partSize,
          generateObjectKey: () => upload.key,
          signRequest: async (request) => {
            this.assertSession()
            const signed = await ops.signFileUpload(upload.file.id, {
              method: request.method,
              uploadId: "uploadId" in request ? request.uploadId : "",
              partNumber: "partNumber" in request ? request.partNumber : 0,
            })
            this.assertSession()
            return { url: signed.url, headers: signed.headers }
          },
        })
        this.uppyFileID = this.uppy.addFile({ name: this.file.name, type: this.file.type, data: this.file })
        if (upload.uploadId) this.uppy.setFileState(this.uppyFileID, { s3Multipart: { uploadId: upload.uploadId, key: upload.key } })
      }
      const uppy = this.uppy
      try {
        await new Promise<void>((resolve, reject) => {
          const progress: Parameters<typeof uppy.on<"upload-progress">>[1] = (_file, value) => {
            this.bytes = value.bytesUploaded
            onProgress(this.bytes)
          }
          const success = () => { cleanup(); resolve() }
          const failure = (_file: unknown, error: Error) => { cleanup(); reject(error) }
          const abort = () => { cleanup(); uppy.cancelAll(); reject(new DOMException("Upload cancelled", "AbortError")) }
          const unsubscribe = subscribeSessionGeneration(abort)
          const cleanup = () => {
            unsubscribe()
            signal.removeEventListener("abort", abort)
            uppy.off("upload-progress", progress)
            uppy.off("upload-success", success)
            uppy.off("upload-error", failure)
          }
          uppy.on("upload-progress", progress)
          uppy.on("upload-success", success)
          uppy.on("upload-error", failure)
          signal.addEventListener("abort", abort, { once: true })
          const operation = this.started ? uppy.retryUpload(this.uppyFileID) : uppy.upload()
          this.started = true
          void operation.catch((error: Error) => failure(undefined, error))
        })
        this.contentUploaded = true
        this.bytes = this.file.size
        onProgress(this.bytes)
        uppy.destroy()
        this.uppy = null
      } catch (error) {
        if (signal.aborted || this.generation !== currentSessionGeneration() || this.workspace !== requestWorkspace()) {
          uppy.destroy()
          this.uppy = null
        }
        throw error
      }
    }
    signal.throwIfAborted()
    this.assertSession()
    return complete(upload.file.id)
  }
}
