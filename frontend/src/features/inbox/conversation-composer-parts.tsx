/** 消息输入区的局部界面：@ 候选与切换备注提示、服务会话的回复对象、引用预览条、附件入口、表情面板。 */
import { useRef, useState, type RefObject } from "react"
import { PaperclipIcon, SmileIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  ConversationType,
  ServiceSource,
  type ConversationMessageReference,
  type InboxConversationData,
} from "@/api"
import { IconTooltip } from "@/components/icon-tooltip"
import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import { useContactName } from "@/hooks/use-contact-name"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { composerToolClass } from "@/features/inbox/composer-tool"
import { messagePreview } from "@/lib/message-preview"
import { cn } from "@/lib/utils"
import composerEmojis from "../../../../internal/publicweb/composer-emojis.json"

import { ConversationAttachmentUpload } from "./conversation-attachment-upload"
import type { ServiceRecipient } from "./conversation-composer-types"
import type { MentionCandidate } from "./use-composer-mentions"

/** 在输入框上方展示 @ 候选列表，或对客模式下切换到内部备注的提示。 */
export function ComposerMentionOverlay({
  candidates,
  activeIndex,
  groupConversation,
  showCandidates,
  showNoteHint,
  onSelect,
  onSwitchToNote,
}: {
  candidates: MentionCandidate[]
  activeIndex: number
  groupConversation: boolean
  showCandidates: boolean
  showNoteHint: boolean
  onSelect: (candidate: MentionCandidate) => void
  onSwitchToNote: () => void
}) {
  const { t } = useTranslation("inbox")
  return (
    <>
      {showCandidates ? (
        <div
          role="listbox"
          aria-label={t(groupConversation ? "messageMentionCandidates" : "noteMentionCandidates")}
          className="absolute bottom-full left-2 z-30 mb-1 max-h-56 min-w-56 overflow-y-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
        >
          {candidates.map((candidate, index) => (
            <button
              key={
                candidate.kind === "all"
                  ? "all"
                  : candidate.target.identityID
              }
              type="button"
              role="option"
              aria-selected={index === activeIndex}
              className={cn(
                "flex min-h-9 w-full items-center rounded-sm px-2 py-1.5 text-left text-sm outline-none hover:bg-accent aria-selected:bg-accent",
              )}
              onPointerDown={(event) => event.preventDefault()}
              onClick={() => onSelect(candidate)}
            >
              {candidate.label}
            </button>
          ))}
        </div>
      ) : null}
      {showNoteHint ? (
        <div
          role="status"
          className="absolute bottom-full left-2 z-30 mb-1 flex items-center gap-3 rounded-md border bg-popover py-1.5 pr-1.5 pl-3 text-sm text-popover-foreground shadow-md"
        >
          {t("noteMentionHint")}
          <Button
            type="button"
            size="sm"
            variant="outline"
            onPointerDown={(event) => event.preventDefault()}
            onClick={onSwitchToNote}
          >
            {t("noteMentionSwitch")}
          </Button>
        </div>
      ) : null}
    </>
  )
}

/** 写明服务会话中本次发送的去向：对客回复发给发起人及其来源，内部备注只给处理人看。 */
export function ComposerRecipient({
  recipient,
  internalNote,
}: {
  recipient: ServiceRecipient
  internalNote: boolean
}) {
  const { t } = useTranslation("inbox")
  const contactName = useContactName()
  // 渠道来源显示渠道名称，单聊和群聊显示来源名称。
  const source =
    recipient.source === ServiceSource.ServiceSourceChannel
      ? recipient.channelName
      : t("filterSourceDirect")
  return (
    <p className="truncate px-3 pt-1.5 text-xs text-muted-foreground">
      {internalNote
        ? t("composerRecipientNote")
        : t("composerRecipient", {
            name: contactName(recipient.name, recipient.contactNumber) || t("unknownSender"),
            source: source ?? "",
          })}
    </p>
  )
}

/** 展示正在回复的消息摘要和取消引用按钮。 */
export function ComposerReplyPreview({
  replyTo,
  disabled,
  onCancel,
}: {
  replyTo: ConversationMessageReference
  disabled: boolean
  onCancel: () => void
}) {
  const { t } = useTranslation("inbox")
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const contactName = useContactName()
  return (
    <div className="flex items-start justify-between gap-3 border-b px-3 py-2 text-xs">
      <div className="min-w-0">
        <p className="font-medium text-foreground">
          {replyTo.deleted
            ? t("messageOriginalDeleted")
            : t("messageReplyingTo", {
                name:
                  personalAgentDisplayName(
                    contactName(replyTo.sender?.displayName, replyTo.sender?.contactNumber),
                    replyTo.sender?.personalResponsibleName,
                  ) || t("unknownSender"),
              })}
        </p>
        <p className="truncate text-muted-foreground">
          {messagePreview(replyTo.body, replyTo.sender?.identityType)}
        </p>
      </div>
      <button
        type="button"
        className={cn(
          "-my-1 min-h-8 shrink-0 px-2 text-muted-foreground hover:text-foreground",
        )}
        disabled={disabled}
        onClick={onCancel}
      >
        {t("messageReplyCancel")}
      </button>
    </div>
  )
}

/** 表情面板：选中后由调用方写入正文并返回光标位置，关闭面板时焦点回到该位置。 */
export function ComposerEmojiPicker({
  disabled,
  inputRef,
  onInsert,
}: {
  disabled: boolean
  inputRef: RefObject<HTMLTextAreaElement | null>
  onInsert: (emoji: string) => number | null
}) {
  const { t } = useTranslation("inbox")
  const [open, setOpen] = useState(false)
  const caretRef = useRef<number | null>(null)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <IconTooltip label={t("emojiPick")}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className={composerToolClass}
            disabled={disabled}
            aria-label={t("emojiPick")}
          >
            <SmileIcon />
          </Button>
        </PopoverTrigger>
      </IconTooltip>
      <PopoverContent
        side="top"
        align="start"
        collisionPadding={8}
        aria-label={t("emojiPick")}
        className={cn(
          "grid max-h-64 gap-0.5 overflow-y-auto p-1.5",
          // 面板不超过可用宽度，列数按 36px 按钮自动填充。
          "w-[min(19rem,var(--radix-popover-content-available-width))] grid-cols-[repeat(auto-fill,2.25rem)]",
        )}
        onCloseAutoFocus={(event) => {
          // 选中表情后焦点回到输入框并定位到插入内容之后。
          const caret = caretRef.current
          const input = inputRef.current
          if (caret === null || !input) return
          caretRef.current = null
          event.preventDefault()
          input.focus()
          input.setSelectionRange(caret, caret)
        }}
      >
        {composerEmojis.map((emoji) => (
          <button
            key={emoji}
            type="button"
            className={cn(
              "flex size-9 items-center justify-center rounded-md text-xl leading-none outline-none hover:bg-accent focus-visible:bg-accent",
            )}
            onClick={() => {
              // 写入成功后记录光标位置并关闭面板。
              const caret = onInsert(emoji)
              if (caret === null) return
              caretRef.current = caret
              setOpen(false)
            }}
          >
            {emoji}
          </button>
        ))}
      </PopoverContent>
    </Popover>
  )
}

/** 附件入口：支持附件的会话展示上传按钮，其余展示禁用的附件按钮。 */
export function ComposerAttachmentTool({
  conversationID,
  conversationType,
  service,
  customerEnabled,
  targetIdentityID,
  agentDraft,
  byteLimit,
  captionLimit,
  replyTo,
  disabled,
  addFilesRef,
  onSent,
  onBeforeSend,
  onCreated,
}: {
  conversationID: string
  conversationType: ConversationType
  service: boolean
  customerEnabled: boolean
  targetIdentityID?: string
  agentDraft?: { conversationID: string; agentIdentityID: string; servedConversationID?: string }
  byteLimit: number
  captionLimit: number
  replyTo: ConversationMessageReference | null
  disabled: boolean
  addFilesRef?: RefObject<((files: File[]) => void) | null>
  onSent: () => void
  onBeforeSend?: () => Promise<boolean>
  onCreated?: (conversation: InboxConversationData | null, conversationID: string) => void
}) {
  const { t } = useTranslation("inbox")
  return (service
    ? customerEnabled
    : conversationType === ConversationType.ConversationTypeDirect ||
      conversationType === ConversationType.ConversationTypeAgent ||
      conversationType === ConversationType.ConversationTypeCopilot ||
      conversationType === ConversationType.ConversationTypeGroup) ? (
    <ConversationAttachmentUpload
      conversationID={conversationID || (agentDraft?.conversationID ?? "")}
      targetIdentityID={targetIdentityID}
      agentIdentityID={agentDraft?.agentIdentityID}
      servedConversationID={agentDraft?.servedConversationID}
      customer={service}
      byteLimit={byteLimit}
      captionLimit={captionLimit}
      replyTo={replyTo ?? null}
      onSent={onSent}
      disabled={disabled}
      addFilesRef={addFilesRef}
      onBeforeSend={onBeforeSend}
      onCreated={(conversation, conversationID) => onCreated?.(conversation, conversationID)}
    />
  ) : (
    <IconTooltip label={t("attachmentAdd")}>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        className={composerToolClass}
        disabled
        aria-label={t("attachmentAdd")}
      >
        <PaperclipIcon />
      </Button>
    </IconTooltip>
  )
}
