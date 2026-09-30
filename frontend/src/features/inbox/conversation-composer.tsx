/** 展示会话消息输入区、工具栏和提及候选。 */
import { useRef, useState, type DragEvent } from "react"
import { ArrowUpIcon, LoaderCircleIcon, MicIcon, StickyNoteIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { MessageVisibility } from "@/api"
import { IconTooltip } from "@/components/icon-tooltip"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { CustomerReplyAssistant } from "./customer-reply-assistant"
import { ComposerTranslation } from "./composer-translation"
import { composerToolClass } from "./composer-tool"
import { resizeComposerInput } from "./composer-input"
import { ComposerAttachmentTool, ComposerEmojiPicker, ComposerMentionOverlay, ComposerRecipient, ComposerReplyPreview } from "./conversation-composer-parts"
import { reconcileMentionAllToken } from "@/lib/mention-token"
import { cn } from "@/lib/utils"
import { useConversationComposer } from "./use-conversation-composer"
import type { ConversationComposerProps } from "./conversation-composer-types"

/** 展示并提交成员会话文本编辑区。 */
export function ConversationComposer(props: ConversationComposerProps) {
  const { t } = useTranslation("inbox")
  const { conversationID, conversationType, service = false, serviceRecipient = null, customerChannel = null, currentIdentityID = "", replyTo = null, onReplyToChange, onVisibilityChange,
    attachmentTargetIdentityID, attachmentAgentDraft, onBeforeSend, onAttachmentConversationCreated, disabledAction = null,
  } = props
  const {
    form, inputRef, inputID, bodyValue, isBodyEmpty, isSubmitting, preparing, internalNote, disabledReason,
    mobile, groupConversation, customerAttachmentSupported, customerAttachmentByteLimit, customerAttachmentCaptionLimit,
    mentionCandidates, activeMentionIndex, mentionQuery, noteMentionHint, selectMention, switchVisibility,
    setMentionAllToken, typingReport, reconcileMentions, updateMentionQuery, setMentionQuery,
    insertEmoji, applyReplySuggestion, submitFromKeyboard, showSubmitting, send,
    replyTranslationAvailable, translationPreviewOpen, setTranslationPreviewOpen, sendTranslation,
  } = useConversationComposer(props)
  const bodyField = form.register("body")
  const addFilesRef = useRef<((files: File[]) => void) | null>(null)
  const [draggingFiles, setDraggingFiles] = useState(false)
  // 拖入文件时拦截浏览器打开文件，附件入口可用时高亮输入区并接收放下的文件。
  const draggingFileData = (event: DragEvent) => event.dataTransfer.types.includes("Files")
  // 渠道客户会话在输入区工具栏提供 AI 写回复入口，不可对客发送时保留显示并禁用。
  const replyAssistant =
    service &&
    customerChannel &&
    conversationID &&
    !internalNote ? (
      <CustomerReplyAssistant
        mobile={mobile}
        conversationID={conversationID}
        currentIdentityID={currentIdentityID}
        draft={bodyValue}
        replyToMessageID={replyTo && !replyTo.deleted ? replyTo.id : ""}
        disabled={isSubmitting || Boolean(disabledReason)}
        onApply={applyReplySuggestion}
      />
    ) : null

  // 输入内容后语音入口换成发送按钮，并保持到本次发送结束。
  const showSend = !isBodyEmpty || isSubmitting
  const bodyInput = (
    <Textarea
      {...bodyField}
      ref={(input) => {
        bodyField.ref(input)
        inputRef.current = input
      }}
      id={inputID}
      readOnly={Boolean(disabledReason) || preparing}
      rows={1}
      aria-label={t(internalNote ? "internalNoteLabel" : "replyLabel")}
      aria-describedby={disabledReason ? `${inputID}-reason` : undefined}
      aria-invalid={form.formState.errors.body ? true : undefined}
      className={cn(
        // 行高贴近字体自然行高，换行前后光标高度保持一致；上下内边距之和保持 16px，下伸部留空由上多下少补偿。
        "max-h-[200px] min-h-10 min-w-0 flex-1 resize-none rounded-none border-0 bg-transparent px-0.5 pt-[11px] pb-[9px] leading-5 shadow-none focus-visible:ring-0 dark:bg-transparent",
        // 正文各端统一 16px，移动端聚焦时不缩放，与访客端一致。
        "md:text-[1rem]",
      )}
      onInput={(event) => {
        resizeComposerInput(event.currentTarget)
      }}
      onChange={(event) => {
        const previousBody = form.getValues("body")
        const { value, selectionStart } = event.currentTarget
        setMentionAllToken((current) =>
          reconcileMentionAllToken(
            current, previousBody, value, selectionStart,
          ),
        )
        bodyField.onChange(event)
        typingReport.input(event.currentTarget.value)
        reconcileMentions(event.currentTarget.value)
        updateMentionQuery(
          event.currentTarget.value,
          event.currentTarget.selectionStart,
        )
      }}
      onClick={(event) =>
        updateMentionQuery(
          event.currentTarget.value,
          event.currentTarget.selectionStart,
        )
      }
      onBlur={(event) => {
        bodyField.onBlur(event)
        setMentionQuery(null)
      }}
      onKeyDown={submitFromKeyboard}
      onPaste={(event) => {
        // 粘贴只含文件时交给附件发送框，带文本的内容按文本粘贴。
        const files = Array.from(event.clipboardData.files)
        if (files.length === 0 || !addFilesRef.current || event.clipboardData.types.includes("text/plain")) return
        event.preventDefault()
        addFilesRef.current(files)
      }}
    />
  )

  return (
    <form
      data-slot="conversation-composer"
      data-conversation-id={conversationID}
      className={cn("shrink-0 bg-background", draggingFiles && "ring-2 ring-primary ring-inset")}
      onSubmit={form.handleSubmit((values) => send(values))}
      onDragOver={(event) => {
        if (!draggingFileData(event)) return
        event.preventDefault()
        setDraggingFiles(Boolean(addFilesRef.current))
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDraggingFiles(false)
      }}
      onDrop={(event) => {
        setDraggingFiles(false)
        if (!draggingFileData(event)) return
        event.preventDefault()
        addFilesRef.current?.(Array.from(event.dataTransfer.files))
      }}
      noValidate
    >
      <div className="relative">
        <ComposerMentionOverlay
          candidates={mentionCandidates}
          activeIndex={activeMentionIndex}
          groupConversation={groupConversation}
          showCandidates={!disabledReason && Boolean(mentionQuery) && mentionCandidates.length > 0}
          showNoteHint={!disabledReason && noteMentionHint}
          onSelect={selectMention}
          onSwitchToNote={() =>
            switchVisibility(MessageVisibility.MessageVisibilityInternal)
          }
        />
        <div
          className={cn(
            internalNote
              ? "bg-note/60"
              : "bg-background",
          )}
        >
          {serviceRecipient && !disabledReason ? (
            <ComposerRecipient recipient={serviceRecipient} internalNote={internalNote} />
          ) : null}
          {replyTo ? (
            <ComposerReplyPreview
              replyTo={replyTo}
              disabled={Boolean(disabledReason)}
              onCancel={() => onReplyToChange?.(null)}
            />
          ) : null}
          {/* 输入区整体铺底色，正文单独用白底并与上下边缘留出间距。 */}
          <div className="flex items-end gap-2 bg-foreground/[0.03] px-2 py-1">
            <div className="mb-1.5 flex items-end">
              {onVisibilityChange ? (
                <IconTooltip label={t("composerModeNote")}>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-pressed={internalNote}
                      className={cn(
                        composerToolClass,
                        internalNote &&
                          "bg-note-active text-note-active-foreground hover:bg-note-active hover:text-note-active-foreground dark:hover:bg-note-active",
                      )}
                      aria-label={t("composerModeNote")}
                      onClick={() =>
                        switchVisibility(
                          internalNote
                            ? MessageVisibility.MessageVisibilityShared
                            : MessageVisibility.MessageVisibilityInternal,
                        )
                      }
                    >
                      <StickyNoteIcon />
                    </Button>
                </IconTooltip>
              ) : null}
              <ComposerAttachmentTool
                conversationID={conversationID}
                conversationType={conversationType}
                service={service}
                customerEnabled={customerAttachmentSupported && !internalNote}
                targetIdentityID={attachmentTargetIdentityID}
                agentDraft={attachmentAgentDraft}
                byteLimit={customerAttachmentByteLimit}
                captionLimit={customerAttachmentCaptionLimit}
                replyTo={replyTo}
                disabled={isSubmitting || Boolean(disabledReason)}
                addFilesRef={addFilesRef}
                onSent={() => onReplyToChange?.(null)}
                onBeforeSend={onBeforeSend}
                onCreated={onAttachmentConversationCreated}
              />
            </div>
            <div className="flex min-w-0 flex-1 items-end rounded-md bg-background px-2">
              {disabledReason ? (
                <>
                  <p
                    id={`${inputID}-reason`}
                    className="min-w-0 flex-1 truncate py-[10px] text-xs leading-5 text-muted-foreground"
                  >
                    {disabledReason}
                  </p>
                  {disabledAction ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="my-1 shrink-0"
                      disabled={disabledAction.busy}
                      onClick={disabledAction.onClick}
                    >
                      {disabledAction.busy ? <LoaderCircleIcon className="animate-spin" /> : null}
                      {disabledAction.label}
                    </Button>
                  ) : null}
                </>
              ) : (
                bodyInput
              )}
            </div>
            <div className="mb-1.5 flex items-end">
              <ComposerEmojiPicker
                disabled={isSubmitting || Boolean(disabledReason)}
                inputRef={inputRef}
                onInsert={insertEmoji}
              />
              {replyAssistant}
              {replyTranslationAvailable ? (
                <ComposerTranslation
                  draft={bodyValue}
                  disabled={isSubmitting || Boolean(disabledReason) || Boolean(replyTo?.deleted)}
                  mobile={mobile}
                  open={translationPreviewOpen}
                  onOpenChange={setTranslationPreviewOpen}
                  onSend={sendTranslation}
                />
              ) : null}
              {showSend ? (
                <IconTooltip label={t(internalNote ? "internalNoteSave" : "messageSend")}>
                  <Button
                    type="submit"
                    variant="ghost"
                    size="icon-sm"
                    className={cn(composerToolClass, "group")}
                    disabled={isSubmitting || Boolean(disabledReason) || isBodyEmpty || replyTo?.deleted}
                    aria-label={t(internalNote ? "internalNoteSave" : "messageSend")}
                    // 点按发送时输入框保持焦点，移动端软键盘不收起。
                    onPointerDown={(event) => event.preventDefault()}
                  >
                    {/* 28px 按钮内绘制 24px 主色实心圆。 */}
                    <span className="flex size-6 items-center justify-center rounded-full bg-primary text-primary-foreground transition-colors group-hover:bg-primary-hover">
                      {showSubmitting ? <LoaderCircleIcon className="size-3.5 animate-spin" /> : <ArrowUpIcon className="size-3.5" />}
                    </span>
                  </Button>
                </IconTooltip>
              ) : (
                <IconTooltip label={t("voiceMessage")}>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    className={composerToolClass}
                    disabled
                    aria-label={t("voiceMessage")}
                  >
                    <MicIcon />
                  </Button>
                </IconTooltip>
              )}
            </div>
          </div>
        </div>
      </div>
    </form>
  )
}
