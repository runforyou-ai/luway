/** 共用附件选择列表与时间线的文件图标和图片展示。 */
import { FileIcon } from "lucide-react"
import type { ReactNode } from "react"
import { formatFileSize } from "@/lib/file-size"
import { cn } from "@/lib/utils"
import { AttachmentName } from "@/components/attachment-name"

/** 展示图片预览或文件图标、名称与大小，图片没有预览地址时在原位置显示状态说明。 */
export function AttachmentContent({
  name,
  byteSize,
  previewURL,
  imageWidth,
  imageHeight,
  action,
  detail,
  footer,
  imageFooterClassName,
  inverted = false,
  onOpen,
  openLabel,
  onImageLoad,
  onImageError,
}: {
  name: string
  byteSize: number
  previewURL?: string
  imageWidth: number
  imageHeight: number
  action?: ReactNode
  detail?: ReactNode
  footer?: ReactNode
  imageFooterClassName?: string
  inverted?: boolean
  onOpen?: () => void
  openLabel?: string
  onImageLoad?: () => void
  onImageError?: () => void
}) {
  if (imageWidth > 0 && imageHeight > 0) {
    return (
      <div
        className="relative max-w-full overflow-hidden rounded-xl ring-1 ring-border"
        style={{
          width: Math.min(imageWidth, 320, (320 * imageWidth) / imageHeight),
          aspectRatio: `${imageWidth} / ${imageHeight}`,
        }}
      >
        {previewURL ? (
          <img
            src={previewURL}
            alt={onOpen ? "" : name}
            className="block size-full object-contain"
            onLoad={onImageLoad}
            onError={onImageError}
          />
        ) : (
          // 没有预览地址时在图片位置显示接收中或接收失败等状态。
          <div className="flex size-full min-h-20 items-center justify-center bg-muted px-3 text-center text-xs text-muted-foreground">
            {detail}
          </div>
        )}
        {onOpen ? (
          <button
            type="button"
            className="absolute inset-0 rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            aria-label={openLabel ?? name}
            onClick={onOpen}
          />
        ) : null}
        {action ? (
          <div className="absolute inset-0 flex items-center justify-center">
            {action}
          </div>
        ) : null}
        {footer ? (
          <div
            className={cn(
              "pointer-events-none absolute right-1.5 bottom-1.5 rounded-full bg-black/45 px-2 py-0.5 text-white transition-opacity",
              imageFooterClassName,
            )}
          >
            {footer}
          </div>
        ) : null}
      </div>
    )
  }
  return (
    <div className="flex min-w-0 items-center gap-3 py-1">
      <div
        className={cn(
          "relative flex size-12 shrink-0 items-center justify-center rounded-full",
          inverted ? "bg-accent-foreground/15 text-accent-foreground" : "bg-primary/15 text-primary",
        )}
      >
        {action ?? (
          <button
            type="button"
            disabled={!onOpen}
            aria-label={openLabel ?? name}
            className="flex size-full items-center justify-center rounded-full disabled:cursor-default"
            onClick={onOpen}
          >
            <FileIcon className="size-6" />
          </button>
        )}
      </div>
      <div className="min-w-0 flex-1 space-y-1">
        <button
          type="button"
          disabled={!onOpen}
          onClick={onOpen}
          aria-label={name}
          title={name}
          className="block w-full min-w-0 text-left text-sm font-medium disabled:cursor-default"
        >
          <AttachmentName name={name} />
        </button>
        <div
          className={cn(
            "grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2 text-xs",
            inverted ? "text-accent-foreground/75" : "text-muted-foreground",
          )}
        >
          <span className="truncate tabular-nums">
            {detail ?? formatFileSize(byteSize)}
          </span>
          {footer}
        </div>
      </div>
    </div>
  )
}
