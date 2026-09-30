/** 创建文件记录并将内容上传到最终存储位置。 */
import {
  CompleteFileUpload,
  CreateFilePartUpload,
  CancelFileUpload,
  CreateFileUpload,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import type {
  FilePurpose,
  FileUpload,
  FileUploadRequest,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"

/** 创建分片直传请求。 */
export const createFilePartUpload = bind(CreateFilePartUpload)

/** 取消未发送的临时文件。 */
export const cancelFileUpload = bind(CancelFileUpload)

/** 创建文件上传请求。 */
export const createFileUpload = bind(CreateFileUpload)

/** 核验并完成文件上传。 */
export const completeFileUpload = bind(CompleteFileUpload)

/** 创建、上传并确认一个临时文件。 */
export async function uploadFile(file: globalThis.File, purpose: FilePurpose) {
  const transfer = new FileTransfer(file, () =>
    createFileUpload({
      purpose,
      fileName: file.name,
      contentType: file.type,
      byteSize: file.size,
    }),
  )
  return transfer.run(
    new AbortController().signal,
    () => {},
    completeFileUpload,
  )
}

/** 执行单个文件的传输，并在当前页面保留已成功的分片位置。 */
export class FileTransfer {
  upload: FileUpload | null = null
  bytes = 0
  private nextPart = 1
  private contentUploaded = false
  private file: globalThis.File
  private prepare: () => Promise<FileUpload>

  /** 保存文件及上传请求的创建方式。 */
  constructor(file: globalThis.File, prepare: () => Promise<FileUpload>) {
    this.file = file
    this.prepare = prepare
  }

  /** 上传剩余内容，再调用所属业务的完成操作。 */
  async run<T>(
    signal: AbortSignal,
    onProgress: (bytes: number) => void,
    complete: (fileID: string) => Promise<T>,
  ): Promise<T> {
    signal.throwIfAborted()
    this.upload ??= await this.prepare()
    signal.throwIfAborted()
    const { partSize, file, request } = this.upload
    try {
      if (partSize > 0) {
        while ((this.nextPart - 1) * partSize < this.file.size) {
          const start = (this.nextPart - 1) * partSize
          const end = Math.min(start + partSize, this.file.size)
          const part = await createFilePartUpload(file.id, {
            partNumber: this.nextPart,
          })
          signal.throwIfAborted()
          await uploadFileSlice(
            part,
            this.file.slice(start, end),
            signal,
            (bytes) => onProgress(start + bytes),
          )
          this.bytes = end
          this.nextPart++
        }
      } else if (!this.contentUploaded) {
        await uploadFileSlice(request, this.file, signal, onProgress)
        this.bytes = this.file.size
      }
      this.contentUploaded = true
      signal.throwIfAborted()
      return await complete(file.id)
    } catch (error) {
      onProgress(this.bytes)
      throw error
    }
  }
}

/** 上传一个文件片段并报告字节进度。 */
export function uploadFileSlice(
  request: FileUploadRequest,
  content: Blob,
  signal: AbortSignal,
  onProgress: (bytes: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    const abort = () => xhr.abort()
    xhr.open(request.method, request.url)
    for (const [name, value] of Object.entries(request.headers ?? {})) {
      if (value !== undefined) xhr.setRequestHeader(name, value)
    }
    xhr.upload.onprogress = (event) => onProgress(event.loaded)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve()
      else reject(new Error(`File upload failed with status ${xhr.status}`))
    }
    xhr.onerror = () => reject(new Error("File upload network error"))
    xhr.onabort = () =>
      reject(new DOMException("Upload cancelled", "AbortError"))
    xhr.onloadend = () => signal.removeEventListener("abort", abort)
    if (signal.aborted) {
      reject(new DOMException("Upload cancelled", "AbortError"))
      return
    }
    signal.addEventListener("abort", abort, { once: true })
    xhr.send(content)
  })
}
