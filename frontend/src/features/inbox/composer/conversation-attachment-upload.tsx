/** 在内部聊天附件模态框中选择文件和说明，发送后交给工作台队列上传。 */
import { useEffect, useRef, type RefObject } from "react"
import { PaperclipIcon, XIcon } from "lucide-react"
import { ScrollArea } from "radix-ui"
import { useTranslation } from "react-i18next"
import { useForm } from "react-hook-form"
import { z } from "zod"
import type { ChannelAttachmentRule, ConversationMessageReference, InboxConversation } from "@/api"
import { IconTooltip } from "@/components/icon-tooltip"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FieldLabel } from "@/components/ui/field"
import { ScrollBar } from "@/components/ui/scroll-area"
import { Textarea } from "@/components/ui/textarea"
import { composerToolClass } from "@/features/inbox/composer/composer-tool"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { cn } from "@/lib/utils"
import { resolveAppPlatform } from "@/platform/app-platform"
import { AttachmentContent } from "@/features/inbox/shared/attachment-content"
import { attachmentAccept, attachmentSelectionLimit, useAttachmentSelection } from "./use-attachment-selection"
import { useAttachmentQueue } from "@/features/inbox/state/attachment-queue-context"
import type { SelectedAttachment } from "@/features/inbox/state/attachment-queue"
import { zodResolver } from "@/lib/zod-resolver"

/** 选择最多一百个文件，发送后交给工作台队列按选择顺序上传并发送；attachmentRules 限定渠道可外发的附件类别，captionLimit 为 0 时不填写说明；可用时在 addFilesRef 登记粘贴与拖入文件的入口。 */
export function ConversationAttachmentUpload({
  conversationID,
  targetIdentityID = "",
  agentIdentityID = "",
  servedConversationID = "",
  customer = false,
  attachmentRules = null,
  captionLimit = 4000,
  replyTo = null,
  disabled,
  addFilesRef,
  onCreated,
  onBeforeSend,
  onSent,
}: {
  conversationID: string
  targetIdentityID?: string
  agentIdentityID?: string
  servedConversationID?: string
  customer?: boolean
  attachmentRules?: ChannelAttachmentRule[] | null
  captionLimit?: number
  replyTo?: ConversationMessageReference | null
  disabled: boolean
  addFilesRef?: RefObject<((files: File[]) => void) | null>
  onCreated: (conversation: InboxConversation | null, conversationID: string) => void
  onBeforeSend?: () => Promise<boolean>
  onSent?: () => void
}) {
  const { t } = useTranslation("inbox")
  const { t: tCommon } = useTranslation("common")
  const queue = useAttachmentQueue()
  const mobile = resolveAppPlatform() === "mobile"
  const selection = useAttachmentSelection(attachmentRules)
  const { selected, selecting } = selection
  const inputRef = useRef<HTMLInputElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const previousCountRef = useRef(0)
  const aliveRef = useMountedRef()
  const form = useForm({
    defaultValues: { description: "" },
    resolver: zodResolver(
      z.object({
        description: z.string().max(captionLimit),
      }),
    ),
    shouldUseNativeValidation: true,
  })
  const description = form.register("description")

  // 追加附件后滚动到列表底部，移除附件时保留浏览位置。
  useEffect(() => {
    if (selected.length > previousCountRef.current && listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight
    }
    previousCountRef.current = selected.length
  }, [selected.length])

  const addAvailable = !disabled && !selecting && Boolean(queue)
  // 附件入口可用时登记粘贴与拖入文件的入口，不可用或卸载时清除。
  useEffect(() => {
    if (!addFilesRef || !addAvailable) return
    addFilesRef.current = (files) => void selection.add(files)
    return () => {
      addFilesRef.current = null
    }
  })

  /** 把文件所有权移交工作台队列，立即关闭选择框。 */
  async function send(values: { description: string }) {
    if (!queue || selection.isSelecting() || selection.current().length === 0) return
    // 发送前回到最新消息窗口，随后展示本地上传气泡。
    if (onBeforeSend && !(await onBeforeSend())) return
    if (!aliveRef.current) return
    // 说明只随最后一个附件发送，其余附件保持独立消息。
    const items = selection.current()
    queue.enqueue(
      items.map((item, index) => ({
        ...item,
        body: index === items.length - 1 ? values.description : "",
      })),
      { conversationID, targetIdentityID, agentIdentityID, servedConversationID, customer, replyTo },
      (conversation, conversationID) => {
        if (aliveRef.current) onCreated(conversation, conversationID)
      },
    )
    selection.release()
    form.reset()
    onSent?.()
  }

  return (
    <>
      <input
        ref={inputRef}
        type="file"
        multiple
        accept={attachmentAccept(attachmentRules)}
        className="hidden"
        aria-label={t("attachmentAdd")}
        onChange={(event) => {
          const files = Array.from(event.currentTarget.files ?? [])
          event.currentTarget.value = ""
          void selection.add(files)
        }}
      />
      <IconTooltip label={t("attachmentAdd")}>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className={composerToolClass}
          disabled={disabled || selecting || form.formState.isSubmitting || !queue}
          aria-label={t("attachmentAdd")}
          onClick={() => inputRef.current?.click()}
        >
          <PaperclipIcon />
        </Button>
      </IconTooltip>
      <Dialog
        open={selected.length > 0}
        onOpenChange={(open) => {
          if (!open && !form.formState.isSubmitting) {
            selection.replace([])
            form.reset()
          }
        }}
      >
        <DialogContent
          ref={dialogRef}
          closeDisabled={form.formState.isSubmitting}
          className="max-h-[85dvh] sm:max-w-lg"
          closeButtonClassName="touch:top-1 touch:right-1 touch:flex touch:size-11 touch:items-center touch:justify-center"
          aria-describedby={undefined}
          onInteractOutside={(event) => event.preventDefault()}
          onOpenAutoFocus={(event) => {
            // 聚焦弹窗容器并保持附件列表滚动位置。
            event.preventDefault()
            dialogRef.current?.focus({ preventScroll: true })
            if (listRef.current) {
              listRef.current.scrollTop = listRef.current.scrollHeight
            }
          }}
        >
          <DialogHeader className="touch:pr-8">
            <DialogTitle>{t("attachmentSend")}</DialogTitle>
          </DialogHeader>
          <form
            className="min-h-0 min-w-0 space-y-9"
            onSubmit={(event) => {
              event.stopPropagation()
              void form.handleSubmit(send)(event)
            }}
          >
            <div className="space-y-5">
              <SelectedAttachmentList
                listRef={listRef}
                items={selected}
                disabled={form.formState.isSubmitting}
                onRemove={(id) => selection.replace(selection.current().filter((value) => value.id !== id))}
              />
              {/* 渠道附件不带说明时不提供说明输入。 */}
              {captionLimit > 0 ? (
                <div className="space-y-2">
                  <FieldLabel htmlFor="attachment-description">
                    {t("attachmentDescription")}
                  </FieldLabel>
                  <Textarea
                    {...description}
                    disabled={form.formState.isSubmitting}
                    id="attachment-description"
                    rows={1}
                    className="min-h-0 max-h-[184px] resize-none leading-6"
                    onChange={(event) => {
                      void description.onChange(event)
                      // 按内容高度加上下边框自适应，超过上限后滚动。
                      const input = event.currentTarget
                      input.style.height = "auto"
                      input.style.height = `${Math.min(input.scrollHeight + input.offsetHeight - input.clientHeight, 184)}px`
                    }}
                    onKeyDown={(event) => {
                      // 移动端换行；Web 与桌面端 Enter 发送，Shift+Enter 换行，组字中保留原生输入。
                      if (mobile || event.key !== "Enter" || event.shiftKey || event.keyCode === 229 || event.nativeEvent.isComposing) return
                      event.preventDefault()
                      event.currentTarget.form?.requestSubmit()
                    }}
                  />
                </div>
              ) : null}
            </div>
            <div className="flex items-center justify-between">
              <Button
                type="button"
                variant="outline"
                className="touch:min-h-11"
                disabled={selected.length >= attachmentSelectionLimit || selecting || form.formState.isSubmitting}
                onClick={() => inputRef.current?.click()}
              >
                {tCommon("actions.add")}
              </Button>
              <div className="flex items-center justify-end gap-2">
                <Button
                  type="button"
                  variant="outline"
                  className="touch:min-h-11"
                  disabled={form.formState.isSubmitting}
                  onClick={() => {
                    selection.replace([])
                    form.reset()
                  }}
                >
                  {tCommon("actions.cancel")}
                </Button>
                <Button type="submit" className="touch:min-h-11" disabled={selecting || form.formState.isSubmitting}>
                  {t("messageSend")}
                </Button>
              </div>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

/** 待发送附件列表：图片带预览，每行末尾可移除。 */
function SelectedAttachmentList({
  listRef,
  items,
  disabled,
  onRemove,
}: {
  listRef: RefObject<HTMLDivElement | null>
  items: SelectedAttachment[]
  disabled: boolean
  onRemove: (id: string) => void
}) {
  const { t } = useTranslation("inbox")
  return (
    <ScrollArea.Root type="auto" className="relative -mx-6 min-w-0">
      <ScrollArea.Viewport
        ref={listRef}
        className="max-h-[45dvh] w-full overscroll-contain [&>div]:!block"
      >
        <div className="space-y-4 py-1 pl-6 pr-8">
          {items.map((item) => (
            <div key={item.id} className="flex min-w-0 items-center gap-3">
              <div
                className={cn(
                  "min-w-0 flex-1",
                  item.imageWidth > 0 &&
                    item.imageHeight > 0 &&
                    "flex justify-center rounded-xl bg-muted p-3",
                )}
              >
                <AttachmentContent
                  name={item.file.name}
                  byteSize={item.file.size}
                  previewURL={item.previewURL}
                  imageWidth={item.imageWidth}
                  imageHeight={item.imageHeight}
                />
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="touch:size-11 touch:shrink-0"
                aria-label={t("attachmentRemove", { name: item.file.name })}
                disabled={disabled}
                onClick={() => onRemove(item.id)}
              >
                <XIcon />
              </Button>
            </div>
          ))}
        </div>
      </ScrollArea.Viewport>
      <ScrollBar />
    </ScrollArea.Root>
  )
}
