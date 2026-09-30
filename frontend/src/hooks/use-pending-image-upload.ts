/** 管理保存业务资料前的图片预览、即时上传和失败重试。 */
import { useEffect, useRef, useState } from "react"

import { uploadFile, type FilePurpose } from "@/api"

type PendingImage = {
  file: File
  previewURL: string
  status: "uploading" | "uploaded" | "failed"
  fileID: string
  upload?: Promise<string>
}

/** 保留当前候选图片，并忽略替换图片或卸载后的上传结果。 */
export function usePendingImageUpload({
  purpose,
  onError,
}: {
  purpose: FilePurpose
  onError: (error: unknown) => void
}) {
  const [pending, setPending] = useState<PendingImage | null>(null)
  const current = useRef<PendingImage | null>(null)

  useEffect(() => () => {
    if (current.current) URL.revokeObjectURL(current.current.previewURL)
    current.current = null
  }, [])

  /** 上传当前候选图片，失败后保留文件供保存时重试。 */
  function start(candidate: PendingImage) {
    candidate.status = "uploading"
    const upload = uploadFile(candidate.file, purpose).then((file) => file.id)
    candidate.upload = upload
    setPending({ ...candidate })
    void upload.then(
      (fileID) => {
        if (current.current !== candidate) return
        candidate.status = "uploaded"
        candidate.fileID = fileID
        candidate.upload = undefined
        setPending({ ...candidate })
      },
      (error) => {
        if (current.current !== candidate) return
        candidate.status = "failed"
        candidate.upload = undefined
        setPending({ ...candidate })
        onError(error)
      },
    )
    return upload
  }

  /** 替换预览并立即上传选中的图片。 */
  function select(file: File) {
    if (current.current) URL.revokeObjectURL(current.current.previewURL)
    const candidate: PendingImage = {
      file,
      previewURL: URL.createObjectURL(file),
      status: "uploading",
      fileID: "",
    }
    current.current = candidate
    void start(candidate)
  }

  /** 释放当前预览，指定文件编号时只释放该次保存使用的图片。 */
  function clear(savedFileID?: string) {
    if (savedFileID !== undefined && (!savedFileID || current.current?.fileID !== savedFileID)) return
    if (current.current) URL.revokeObjectURL(current.current.previewURL)
    current.current = null
    setPending(null)
  }

  /** 返回当前图片编号，等待进行中的上传或重试失败的上传；上传失败时返回 null，错误已由 onError 展示。 */
  async function ensureUploaded(): Promise<string | null> {
    const candidate = current.current
    if (!candidate) return ""
    if (candidate.fileID) return candidate.fileID
    return (candidate.upload ?? start(candidate)).catch(() => null)
  }

  return { pending, select, clear, ensureUploaded }
}
