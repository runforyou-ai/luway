/** 在时间线中展示附件、图片、说明和发送方本地的上传状态。 */
import { ClockIcon, RotateCcwIcon, XIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useState, type ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import {
  getAttachmentDownload,
  MessageAttachmentTransferStatus,
  type MessageAttachment,
} from "@/api"
import { useResource } from "@/hooks/use-resource"
import { resourceKeys } from "@/hooks/resource-keys"
import { formatFileSize } from "@/lib/file-size"
import { cn } from "@/lib/utils"
import { recoverSession } from "@/lib/session-navigation"
import { resolveAppPlatform } from "@/platform/app-platform"
import { openExternalURL } from "@/platform/external-navigation"
import { AttachmentContent } from "./attachment-content"
import { useAttachmentJob, useAttachmentQueue } from "@/contexts/attachment-queue-context"

/** 用圆环表示上传进度，发送者可在原位置取消或重试。 */
export function ConversationAttachment({
  attachment,
  body,
  conversationID,
  messageID,
  originatedAt,
  timeLabel,
  timeTitle,
  incoming,
  bubbleClassName,
  retryDisabled = false,
  renderDeliveryState,
}: {
  attachment: MessageAttachment
  body: string
  conversationID: string
  messageID: string
  originatedAt: string
  timeLabel: string
  timeTitle: string
  incoming: boolean
  bubbleClassName: string
  retryDisabled?: boolean
  renderDeliveryState?: (className?: string) => ReactNode
}) {
  const { t } = useTranslation(["inbox", "common"])
  const navigate = useNavigate()
  const mobile = resolveAppPlatform() === "mobile"
  const [previewOpen, setPreviewOpen] = useState(false)
  const [failedPreview, setFailedPreview] = useState<string | null>(null)
  const [previewVersion, setPreviewVersion] = useState(0)
  const queue = useAttachmentQueue()
  const job = useAttachmentJob(messageID, attachment.id)
  // 已保存的附件均已上传完成，本地附件按上传任务阶段展示进度、取消和失败。
  const ready = !job || job.stage === "sent"
  // 外部渠道媒体在内容取回完成前没有可读文件。
  const transferred =
    attachment.transferStatus === MessageAttachmentTransferStatus.MessageAttachmentTransferReady
  const failed = job?.stage === "failed"
  const cancellable =
    job?.stage === "queued" ||
    job?.stage === "uploading" ||
    job?.stage === "uploaded"
  const failedLabel = t(job?.fileID ? "messageSendError" : "attachmentUploadFailed")
  const image = attachment.imageWidth > 0 && attachment.imageHeight > 0
  const preview = useResource(
    resourceKeys.attachmentDownload(conversationID, messageID),
    () => getAttachmentDownload(conversationID, messageID),
    {
      enabled: ready && transferred && image && Boolean(conversationID),
      staleTime: 0,
      refetchOnWindowFocus: true,
    },
  )
  const previewFailed = failedPreview !== null && failedPreview === preview.data?.previewUrl
  const progress = attachment.byteSize
    ? Math.min((job?.bytes ?? 0) / attachment.byteSize, 1)
    : 0

  /** 点击文件图标、名称或图片预览中的下载按钮后，下载已完成的附件。 */
  async function download() {
    try {
      const request = await getAttachmentDownload(conversationID, messageID)
      if (resolveAppPlatform() === "web") {
        const anchor = document.createElement("a")
        anchor.href = request.url
        anchor.download = attachment.name
        document.body.append(anchor)
        anchor.click()
        anchor.remove()
      } else {
        await openExternalURL(request.url)
      }
    } catch (error) {
      if (!recoverSession(error, navigate))
        toast.error(t("attachmentDownloadFailed"))
    }
  }

  const control = ready ? null : (
    <div
      className={cn("relative flex size-12 items-center justify-center rounded-full", image && "bg-black/45 text-white")}
    >
      <svg
        viewBox="0 0 48 48"
        className={`absolute inset-0 size-full -rotate-90 ${job?.stage === "queued" ? "animate-spin" : ""}`}
        aria-hidden="true"
      >
        <circle
          cx="24"
          cy="24"
          r="21"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          opacity="0.25"
        />
        {!failed ? (
          <circle
            cx="24"
            cy="24"
            r="21"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeDasharray={`${Math.max(progress, 0.06) * 132} 132`}
          />
        ) : null}
      </svg>
      {!incoming && job && queue && (failed || cancellable) ? (
        <button
          type="button"
          className="relative flex size-full items-center justify-center rounded-full disabled:opacity-50"
          aria-label={failed ? t("common:actions.retry") : t("attachmentCancel")}
          disabled={failed && retryDisabled}
          onClick={() => {
            if (failed) queue.retry(job.id)
            else queue.cancel(job.id)
          }}
        >
          {failed ? (
            <RotateCcwIcon className="size-5" />
          ) : (
            <XIcon className="size-6" />
          )}
        </button>
      ) : (
        <ClockIcon className="size-5" aria-label={t("messageSending")} />
      )}
    </div>
  )
  // 无正文图片的投递状态在图片下方独立展示。
  const deliveryBelowImage = image && !body
  const footer = (
    <span className="inline-flex items-center gap-1 whitespace-nowrap text-[10px]">
      <time dateTime={originatedAt} title={timeTitle}>
        {timeLabel}
      </time>
      {!incoming && !ready ? <ClockIcon className="size-3.5" /> : null}
      {ready && !deliveryBelowImage ? renderDeliveryState?.() : null}
    </span>
  )
  const detail = !ready
    ? failed
      ? failedLabel
      : `${formatFileSize(job?.bytes ?? 0)} / ${formatFileSize(attachment.byteSize)}`
    : transferred
      ? undefined
      : attachment.transferStatus === MessageAttachmentTransferStatus.MessageAttachmentTransferFailed
        ? t("attachmentTransferFailed")
        : t("attachmentTransferPending")
  // 文件打开下载地址，移动端图片打开带下载按钮的应用内预览。
  const canPreview = ready && transferred && mobile && image && Boolean(preview.data?.previewUrl)
  const canDownload = ready && transferred && !(mobile && image)
  const previewRetry = image && (preview.error || previewFailed) ? (
    <button
      type="button"
      className="shrink-0 rounded-md bg-background/90 px-3 py-2 text-xs text-foreground shadow-sm disabled:opacity-50"
      disabled={preview.refreshing}
      onClick={async () => {
        const result = await preview.refresh()
        if (result.error) return
        setFailedPreview(null)
        setPreviewVersion((version) => version + 1)
      }}
    >
      {t("attachmentPreviewRetry")}
    </button>
  ) : null
  const bubble = cn("rounded-2xl px-3 py-2", bubbleClassName)
  return (
    <>
      <div
        // 图片不加气泡并按收发方向对齐，非图片附件整体使用文字气泡。
        className={cn(
          "min-w-0 max-w-full",
          image
            ? cn("flex flex-col gap-1 text-foreground", incoming ? "items-start" : "items-end")
            : cn("w-80", bubble),
        )}
        data-attachment-status={ready ? "ready" : failed ? "failed" : "uploading"}
      >
        <AttachmentContent
          key={previewVersion}
          name={attachment.name}
          byteSize={attachment.byteSize}
          imageWidth={attachment.imageWidth}
          imageHeight={attachment.imageHeight}
          previewURL={preview.data?.previewUrl || job?.previewURL}
          action={control ?? previewRetry}
          detail={detail}
          footer={body ? undefined : footer}
          inverted={!incoming}
          imageFooterClassName={
            ready && !mobile
              ? "opacity-0 group-hover/message-row:opacity-100 group-focus-within/message-row:opacity-100"
              : undefined
          }
          openLabel={canPreview ? t("attachmentPreview", { name: attachment.name }) : undefined}
          onOpen={canPreview || canDownload ? () => {
            if (canPreview) setPreviewOpen(true)
            else void download()
          } : undefined}
          onImageLoad={() => {
            setFailedPreview(null)
            if (preview.data?.previewUrl && job) queue?.releasePreview(job.id)
          }}
          onImageError={() => setFailedPreview(preview.data?.previewUrl ?? null)}
        />
        {image && failed ? (
          <p className="text-xs text-muted-foreground">
            {failedLabel}
          </p>
        ) : null}
        {ready && deliveryBelowImage
          ? renderDeliveryState?.("rounded-full bg-accent px-2 py-0.5 text-accent-foreground")
          : null}
        {body ? (
          // 图片正文单独成气泡，非图片正文留在附件气泡内，时间与正文同行。
          <div className={cn("min-w-0 after:block after:clear-both after:content-['']", image ? cn("max-w-80", bubble) : "pt-2")}>
            <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">{body}</span>
            <div className={cn("float-right ml-2 translate-y-0.5", incoming ? "text-muted-foreground" : "text-accent-foreground/75")}>{footer}</div>
          </div>
        ) : null}
      </div>
      {mobile && image ? (
        <Dialog open={previewOpen} onOpenChange={setPreviewOpen}>
          <DialogContent
            // 纵向排列且不滚动，高度不足时只压缩图片，标题与下载按钮保持可见。
            className="flex max-h-[85dvh] flex-col overflow-hidden p-4"
            closeButtonClassName="top-1 right-1 flex size-11 items-center justify-center"
            aria-describedby={undefined}
          >
            <DialogHeader className="shrink-0 pr-8">
              <DialogTitle className="break-all">{attachment.name}</DialogTitle>
            </DialogHeader>
            <img
              key={previewVersion}
              src={preview.data?.previewUrl}
              alt={attachment.name}
              className="max-h-[65dvh] min-h-0 w-full flex-1 object-contain"
              onError={() => setFailedPreview(preview.data?.previewUrl ?? null)}
            />
            {previewRetry}
            <Button variant="outline" className="min-h-11 w-full" onClick={() => void download()}>
              {t("attachmentDownload")}
            </Button>
          </DialogContent>
        </Dialog>
      ) : null}
    </>
  )
}
