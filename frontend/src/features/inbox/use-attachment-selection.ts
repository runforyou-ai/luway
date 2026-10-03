/** 附件模态框中待发送文件的选择、本地图片预览与释放。 */
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { useMountedRef } from "@/hooks/use-mounted-ref"
import type { SelectedAttachment } from "@/lib/attachment-queue"
import { formatFileSize } from "@/lib/file-size"

/** 单次最多选择的附件数量。 */
export const attachmentSelectionLimit = 100

/** 服务端提供内嵌预览的图片类型，其余图片按文件发送。 */
const inlineImageTypes = new Set(["image/jpeg", "image/png", "image/gif", "image/webp"])

/** 维护按选择顺序排列的附件；byteLimit 大于 0 时过滤超过渠道字节上限的文件。卸载时释放全部预览。 */
export function useAttachmentSelection(byteLimit: number) {
  const { t } = useTranslation("inbox")
  const [selected, setSelected] = useState<SelectedAttachment[]>([])
  const selectedRef = useRef<SelectedAttachment[]>([])
  const selectingRef = useRef(false)
  const selectionRevision = useRef(0)
  const [selecting, setSelecting] = useState(false)
  const aliveRef = useMountedRef()

  useEffect(() => {
    return () => {
      for (const item of selectedRef.current)
        if (item.previewURL) URL.revokeObjectURL(item.previewURL)
    }
  }, [])

  /** 释放移除的预览，保持剩余文件的选择顺序。 */
  function replace(items: SelectedAttachment[]) {
    if (!items.length) selectionRevision.current++
    for (const item of selectedRef.current)
      if (!items.includes(item) && item.previewURL)
        URL.revokeObjectURL(item.previewURL)
    selectedRef.current = items
    setSelected(items)
  }

  /** 读取本地图片尺寸并生成预览后追加到末尾；清空选择后完成的读取结果丢弃。 */
  async function add(files: File[]) {
    if (selectingRef.current) return
    if (selectedRef.current.length + files.length > attachmentSelectionLimit) {
      toast.error(t("attachmentLimit"))
      return
    }
    // 超过渠道字节上限的文件不进入上传队列。
    if (byteLimit > 0) {
      const oversized = files.filter((file) => file.size > byteLimit)
      if (oversized.length > 0) {
        toast.error(t("attachmentTooLarge", { size: formatFileSize(byteLimit) }))
        files = files.filter((file) => file.size <= byteLimit)
        if (files.length === 0) return
      }
    }
    selectingRef.current = true
    setSelecting(true)
    const revision = selectionRevision.current
    const added: SelectedAttachment[] = []
    try {
      for (const file of files) {
        const item: SelectedAttachment = {
          id: crypto.randomUUID(),
          file,
          previewURL: "",
          imageWidth: 0,
          imageHeight: 0,
        }
        if (inlineImageTypes.has(file.type)) {
          const url = URL.createObjectURL(file)
          const image = new Image()
          image.src = url
          try {
            await image.decode()
            item.previewURL = url
            item.imageWidth = image.naturalWidth
            item.imageHeight = image.naturalHeight
          } catch {
            URL.revokeObjectURL(url)
          }
        }
        added.push(item)
      }
      if (!aliveRef.current || revision !== selectionRevision.current) {
        for (const item of added)
          if (item.previewURL) URL.revokeObjectURL(item.previewURL)
        return
      }
      selectedRef.current = [...selectedRef.current, ...added]
      setSelected(selectedRef.current)
    } finally {
      selectingRef.current = false
      if (aliveRef.current) setSelecting(false)
    }
  }

  /** 附件已移交上传队列后清空选择，预览由队列负责释放。 */
  function release() {
    selectedRef.current = []
    setSelected([])
  }

  return {
    selected,
    selecting,
    /** 是否仍在读取刚选择的文件。 */
    isSelecting: () => selectingRef.current,
    /** 当前附件，包含尚未提交渲染的变更。 */
    current: () => selectedRef.current,
    add,
    replace,
    release,
  }
}
