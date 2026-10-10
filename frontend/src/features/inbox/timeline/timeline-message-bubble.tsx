/** 时间线中的消息气泡：发送者、头像、引用块、正文、时间与投递状态，以及回复和复制操作。 */
import { useMemo, useRef, type ReactNode } from "react"
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  ChatSubjectKind,
  ConversationType,
  MessageAttachmentTransferStatus,
  MessageType,
  MessageVisibility,
  WorkspaceIdentityType,
  saveConversationAttachment,
  type ConversationMessageReference,
  type CurrentUser,
} from "@/api"
import { MessageMarkdown } from "@/components/message-markdown"
import { ProfileAvatar } from "@/components/profile-avatar"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { useContactName } from "@/hooks/use-contact-name"
import { usePersonalAgentDisplayName } from "@/hooks/use-personal-agent-display-name"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { isAIIdentityType } from "@/lib/identity-type"
import { mentionTokenPattern } from "@/lib/mention-token"
import type { OutgoingConversationDraft } from "@/features/inbox/state/outgoing-message-store"
import { cn } from "@/lib/utils"
import { resolveAppPlatform } from "@/platform/app-platform"
import { openExternalURL } from "@/platform/system"

import { AgentProcess } from "./agent-process"
import { LocalAgentTurn } from "./local-agent-turn"
import { ConversationAttachment } from "./conversation-attachment"
import { ChannelDeliveryState } from "./channel-delivery-state"
import type { TimelineDateFormatters } from "./timeline-grouping"
import { messageMentionNames, type TimelineMessage } from "./timeline-messages"
import { useMessageTranslation, type MessageTranslationView } from "./message-translation"
import { MessageReplyQuote, MessageTimeMeta, translationToggleLabel } from "./timeline-message-meta"

/** 消息气泡依赖的会话上下文、投递状态与操作入口。 */
export type TimelineMessageBubbleContext = {
  conversationID: string
  conversationType: ConversationType
  /** 处理方查看服务会话时为发起人聊天主体编号。 */
  requesterChatSubjectID: string | null
  /** 发起人查看服务聊天时为接待的 AI 员工名称。 */
  behalfAgentName: string | null
  currentUser: CurrentUser
  formatters: TimelineDateFormatters
  highlighted: boolean
  customerDeliveries: boolean
  sendingText: boolean
  retryFailedMessageDisabled: boolean
  onRetryFailedMessage?: (message: OutgoingConversationDraft) => void
  onDiscardFailedMessage?: (clientMessageID: string) => void
  onReplyMessage?: (
    message: ConversationMessageReference,
    visibility: MessageVisibility,
  ) => void
  noteReplyEnabled: boolean
  customerReplyUnavailable: boolean
  replyVisibility: MessageVisibility
  onFollowReference: (messageID: string) => Promise<void>
  onToggleProcess: () => void
  renderCodeBlock?: (language: string, code: string) => ReactNode | undefined
}

/** 在消息正文中强调结构化提醒。 */
function renderMessageBody(message: TimelineMessage, body: string) {
  const names = messageMentionNames(message)
  if (names.length === 0) return body
  const mentioned = new Set(names.map((name) => `@${name}`))
  const parts = body.split(
    new RegExp(`(${mentionTokenPattern(names)})`, "gu"),
  )
  return parts.map((part, index) =>
    mentioned.has(part) ? (
      <span key={`${part}:${index}`} className="font-semibold underline">
        {part}
      </span>
    ) : (
      part
    ),
  )
}

/** 气泡组件的属性：会话上下文加单条消息及其分组位置。 */
type TimelineMessageBubbleProps = TimelineMessageBubbleContext & {
  message: TimelineMessage
  date: Date
  spaced: boolean
  startsGroup: boolean
  endsGroup: boolean
}

/** 展示一条非系统事件消息的气泡及其右键菜单。 */
export function TimelineMessageBubble(props: TimelineMessageBubbleProps) {
  const {
    message,
    date,
    spaced,
    startsGroup,
    endsGroup,
    conversationID,
    conversationType,
    requesterChatSubjectID,
    behalfAgentName,
    currentUser,
    formatters,
    highlighted,
    onReplyMessage,
    onDiscardFailedMessage,
    noteReplyEnabled,
    customerReplyUnavailable,
    replyVisibility,
  } = props
  const { t } = useTranslation(["inbox", "common"])
  const personalAgentDisplayName = usePersonalAgentDisplayName()
  const contactName = useContactName()
  const reportError = useRequestErrorReporter()
  const rowRef = useRef<HTMLElement>(null)
  // 内容已就绪的附件可以保存到会话共享文件区。
  const savableAttachment = !message.local &&
    message.attachment?.transferStatus === MessageAttachmentTransferStatus.MessageAttachmentTransferReady
  const saveAttachment = useMutation({
    mutationFn: () => saveConversationAttachment(conversationID, message.id),
  })
  const currentIdentityID = currentUser.identityId
  // 右侧 AI 助手面板宽度有限，消息不展示头像，改在气泡上方标出发送者。
  const copilot = conversationType === ConversationType.Copilot
  // 移动端气泡禁止文本选择，消息菜单项使用触屏尺寸，回复入口只通过长按菜单提供。
  const mobile = resolveAppPlatform() === "mobile"
  const menuItemClassName = "touch:min-h-11"
  const agentError = message.type === MessageType.AgentError
  const agentCancelled = message.type === MessageType.AgentCancelled
  const agentNotice = agentError || agentCancelled
  // 发起人经外部平台发送的不支持消息只显示占位文案，不可引用。
  const unsupported = message.type === MessageType.Unsupported
  // 处理方视角中发起人的发言在左侧，其余视角中他人的发言在左侧。
  const incoming = message.local
    ? false
    : requesterChatSubjectID === null
      ? !message.sender ||
        message.sender.sourceId !== currentIdentityID
      : !message.sender ||
        message.sender.chatSubjectId === requesterChatSubjectID
  const translation = useMessageTranslation(
    message,
    message.sender?.kind === ChatSubjectKind.Contact,
    rowRef,
  )
  const sentByCurrentIdentity =
    !message.local &&
    requesterChatSubjectID === null &&
    message.sender?.sourceId === currentIdentityID
  const senderName =
    (message.local || sentByCurrentIdentity
      ? t("messageSenderYou")
      : personalAgentDisplayName(
          contactName(message.sender?.displayName, message.sender?.contactNumber),
          message.sender?.personalResponsibleName,
        )) || t("unknownSender")
  // 从身份资料生成头像及默认头像。
  const useCurrentUserAvatar =
    message.local ||
    (message.sender?.kind ===
      ChatSubjectKind.WorkspaceIdentity &&
      message.sender.sourceId === currentIdentityID)
  // 回复与复制使用气泡当前显示的正文，附件消息没有说明时取文件名。
  const referenceBody = translation.body || message.attachment?.name || ""
  const internalNote =
    message.visibility === MessageVisibility.Internal
  // 引用落入的输入模式：当前处于备注模式，或这条消息不能用于对客回复时，都写入内部备注。
  const quoteAsNote =
    noteReplyEnabled &&
    (replyVisibility === MessageVisibility.Internal ||
      customerReplyUnavailable ||
      !message.canReply)
  const quoteDisabled = quoteAsNote
    ? !message.canNoteReply
    : !message.canReply
  const quoteVisibility = quoteAsNote
    ? MessageVisibility.Internal
    : MessageVisibility.Shared
  // 悬停回复按钮与右键菜单共用同一个引用入口。
  const quoteMessage = () =>
    onReplyMessage?.(
      {
        id: message.id,
        type: message.type,
        visibility: message.visibility,
        body: referenceBody,
        sender: message.sender,
        deleted: false,
      },
      quoteVisibility,
    )
  // 文字气泡与附件气泡共用同一套方向配色和组尾圆角，内部备注使用区别于对客消息的常驻样式。
  const bubbleClassName = cn(
    internalNote
      ? "border border-dashed border-note-border bg-note text-foreground"
      : incoming || agentNotice
        ? "border bg-muted text-foreground shadow-xs"
        : "bg-accent text-accent-foreground",
    endsGroup && (incoming ? "rounded-bl-sm" : "rounded-br-sm"),
  )

  /** 复制一条文本消息的正文。 */
  async function copyMessageText(body: string) {
    try {
      await navigator.clipboard.writeText(body)
      toast.success(t("messageCopySuccess"))
    } catch (copyError) {
      console.warn("复制消息文本失败", copyError)
      toast.error(t("messageCopyError"))
    }
  }

  return (
    <ContextMenu>
      <article
        ref={rowRef}
        data-message-id={message.local ? undefined : message.id}
        tabIndex={-1}
        className={cn(
          highlighted &&
            "message-location-highlight",
          "group/message-row flex items-start gap-2",
          spaced && (startsGroup ? "mt-3" : "mt-1"),
          incoming ? "justify-start" : "justify-end",
        )}
        aria-label={`${senderName} ${formatters.full.format(date)}`}
      >
        <div
          className={cn(
            "flex min-w-0 max-w-[75%] flex-col gap-1",
            (message.agentProcess || message.localAgentTurns.length > 0) && "max-w-[min(36rem,85%)] sm:max-w-[min(36rem,75%)]",
            incoming ? "items-start" : "items-end",
            !copilot && (incoming ? "ml-9" : "mr-9"),
          )}
        >
          {(copilot ||
            (conversationType === ConversationType.Group && incoming)) &&
          startsGroup ? (
            <span className="max-w-full truncate text-xs font-medium text-foreground">
              {senderName}
            </span>
          ) : null}
          {/* 发起人的服务聊天中真人处理人的发言标注代 AI 员工处理。 */}
          {behalfAgentName && incoming && startsGroup &&
          message.sender?.identityType === WorkspaceIdentityType.User ? (
            <span className="max-w-full truncate text-xs font-medium text-foreground">
              {t("messageSenderOnBehalf", { name: senderName, agent: behalfAgentName })}
            </span>
          ) : null}
          {internalNote && startsGroup ? (
            <span className="max-w-full truncate text-xs font-medium text-note-foreground">
              {t("internalNoteSender", { name: senderName })}
            </span>
          ) : null}
          <div className="relative min-w-0 max-w-full">
            {endsGroup && !copilot ? (
              <ProfileAvatar
                title={senderName}
                name={useCurrentUserAvatar
                  ? currentUser.displayName
                  : message.sender?.displayName}
                imageURL={useCurrentUserAvatar
                  ? currentUser.avatarUrl
                  : message.sender?.avatarUrl}
                fallback={isAIIdentityType(message.sender?.identityType)
                  ? "agent"
                  : "person"}
                seed={useCurrentUserAvatar ? null : message.sender?.contactNumber}
                className={cn(
                  "absolute bottom-0 size-7",
                  incoming ? "right-full mr-2" : "left-full ml-2",
                )}
              />
            ) : null}
            <ContextMenuTrigger asChild>
              <div className={cn("group/message relative max-w-full", mobile && "select-none")}>
                {!message.local && !agentNotice && !unsupported && onReplyMessage && !mobile ? (
                  <button
                    type="button"
                    disabled={quoteDisabled}
                    className={cn(
                      incoming ? "-right-2" : "-left-2",
                      "disabled:cursor-not-allowed disabled:opacity-50 pointer-events-none absolute top-0 z-10 -translate-y-1/2 whitespace-nowrap rounded-lg border bg-background px-2 py-1 text-xs text-foreground opacity-0 shadow-sm transition-opacity group-focus-within/message:pointer-events-auto group-focus-within/message:opacity-100 group-hover/message:pointer-events-auto group-hover/message:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100",
                    )}
                    onClick={() => quoteMessage()}
                  >
                    {t("messageReply")}
                  </button>
                ) : null}
                <MessageBubbleContent
                  {...props}
                  body={translation.body}
                  translation={translation.view}
                  incoming={incoming}
                  internalNote={internalNote}
                  bubbleClassName={bubbleClassName}
                />
              </div>
            </ContextMenuTrigger>
          </div>
        </div>
      </article>
      <ContextMenuContent>
        {!message.local && !agentNotice && !unsupported && onReplyMessage ? (
          <ContextMenuItem
            className={menuItemClassName}
            disabled={quoteDisabled}
            onSelect={() => quoteMessage()}
          >
            {t("messageReply")}
          </ContextMenuItem>
        ) : null}
        {translation.view.status === "translated" ? (
          <ContextMenuItem className={menuItemClassName} onSelect={translation.view.toggle}>
            {translationToggleLabel(translation.view.showingOriginal, !incoming, t)}
          </ContextMenuItem>
        ) : null}
        <ContextMenuItem
          className={menuItemClassName}
          onSelect={() => void copyMessageText(agentNotice ? t(agentError ? "agentRunFailed" : "agentReplyStopped") : unsupported ? t("unsupportedMessage") : referenceBody)}
        >
          {t("messageCopyText")}
        </ContextMenuItem>
        {savableAttachment ? (
          <ContextMenuItem
            className={menuItemClassName}
            disabled={saveAttachment.isPending}
            onSelect={() => saveAttachment.mutate(undefined, {
              onSuccess: () => toast.success(t("attachmentSavedToFiles")),
              onError: (error) => reportError(error, { log: "保存附件到会话文件", fallback: t("attachmentSaveToFilesFailed") }),
            })}
          >
            {t("attachmentSaveToFiles")}
          </ContextMenuItem>
        ) : null}
        {/* 未发出的失败文本消息可从时间线移除。 */}
        {message.deliveryStatus === "failed" && message.clientMessageID && !message.attachment && onDiscardFailedMessage ? (
          <>
            <ContextMenuSeparator />
            <ContextMenuItem
              className={menuItemClassName}
              destructive
              onSelect={() => onDiscardFailedMessage(message.clientMessageID!)}
            >
              {t("common:actions.delete")}
            </ContextMenuItem>
          </>
        ) : null}
      </ContextMenuContent>
    </ContextMenu>
  )
}

/** 展示气泡内的引用块、Agent 过程、正文、时间与投递状态。 */
function MessageBubbleContent({
  message,
  body,
  translation,
  date,
  incoming,
  internalNote,
  bubbleClassName,
  conversationID,
  formatters,
  customerDeliveries,
  sendingText,
  retryFailedMessageDisabled,
  onRetryFailedMessage,
  onFollowReference,
  onToggleProcess,
  renderCodeBlock,
}: TimelineMessageBubbleProps & {
  body: string
  translation: MessageTranslationView
  incoming: boolean
  internalNote: boolean
  bubbleClassName: string
}) {
  const { t, i18n } = useTranslation(["inbox", "common"])
  // 提醒名单随消息对象缓存，正文组件按名单引用判断是否重新解析。
  const mentionNames = useMemo(() => messageMentionNames(message), [message])
  const agentError = message.type === MessageType.AgentError
  const agentNotice = agentError || message.type === MessageType.AgentCancelled
  const failedDraft =
    message.deliveryStatus === "failed" && message.clientMessageID
      ? {
          clientMessageID: message.clientMessageID,
          visibility: message.visibility,
          body: message.body,
          originatedAt: message.originatedAt,
          replyTo: message.replyTo,
          mentions: message.draftMentions,
          mentionAll: message.mentionAll,
          mentionAllToken: message.mentionAllToken,
        }
      : null
  // 有文本发送中时禁用文本重试，附件重试由上传队列排队；内部备注的重试不受对客发送资格限制，仍与发送中的文本互斥。
  const messageRetryDisabled = internalNote ? sendingText : retryFailedMessageDisabled || sendingText
  // 处理方发往外部渠道的文本与附件共用同一份投递状态，发起人自己的发言（含成员发起人）没有投递状态。
  const renderDeliveryState = customerDeliveries && !incoming && !agentNotice && !internalNote && (message.local || message.sender?.kind === ChatSubjectKind.WorkspaceIdentity) ? (className?: string) => (
    <ChannelDeliveryState
      className={className}
      conversationID={conversationID}
      delivery={message.delivery}
      localFailed={!message.attachment && message.deliveryStatus === "failed"}
      onRetryLocal={failedDraft && onRetryFailedMessage ? () => onRetryFailedMessage(failedDraft) : undefined}
      retryLocalDisabled={messageRetryDisabled}
    />
  ) : undefined
  return (
    <div
      className={cn(
        "min-w-0 max-w-full text-sm break-words [overflow-wrap:anywhere]",
        !message.attachment && cn("rounded-2xl px-3 py-2", bubbleClassName),
      )}
    >
      {message.replyTo ? (
        <MessageReplyQuote
          replyTo={message.replyTo}
          // 内部备注与附件消息的引用块在对客气泡外，按所在背景取色。
          outside={incoming || internalNote || Boolean(message.attachment)}
          onFollow={onFollowReference}
        />
      ) : null}
      {message.agentProcess ? (
        <AgentProcess process={message.agentProcess} onPrimary={!incoming && !agentNotice} inBubble onToggle={onToggleProcess} />
      ) : null}
      {/* 本机 Agent 的回复标明来源。 */}
      {message.localAgentReply && !agentNotice ? (
        <p className="mb-1 text-xs text-muted-foreground">{t("localAgentReplyFrom", { agent: message.localAgentReply.localAgent })}</p>
      ) : null}
      {/* 时间跟随正文末行，正文按整行宽度排版。 */}
      <div
        className="app-message-body relative min-w-0 after:block after:clear-both after:content-['']"
        data-delivery={Boolean(renderDeliveryState || message.deliveryStatus) || undefined}
        data-translation={translation.status !== "none" || undefined}
      >
        {agentNotice && message.localAgentReply ? (
          <span className={agentError ? "text-destructive" : "text-muted-foreground"}>
            {agentError
              ? t("localAgentFailed", { agent: message.localAgentReply.localAgent, reason: body })
              : t("localAgentStopped", { agent: message.localAgentReply.localAgent })}
          </span>
        ) : agentNotice ? (
          <span className={agentError ? "text-destructive" : "text-muted-foreground"}>
            {t(agentError ? "agentRunFailed" : "agentReplyStopped")}
          </span>
        ) : message.type === MessageType.Unsupported ? (
          <span className="text-muted-foreground">{t("unsupportedMessage")}</span>
        ) : message.attachment ? (
          <ConversationAttachment retryDisabled={retryFailedMessageDisabled} body={body} attachment={message.attachment} conversationID={conversationID} messageID={message.persistedMessageID ?? message.id}
            originatedAt={message.originatedAt} timeLabel={formatters.clock.format(date)} timeTitle={formatters.full.format(date)} incoming={incoming} bubbleClassName={bubbleClassName} renderDeliveryState={renderDeliveryState} />
        ) : isAIIdentityType(message.sender?.identityType) ? (
          <div className="min-w-0">
            <MessageMarkdown
              locale={i18n.language}
              mentions={mentionNames}
              onOpenLink={openExternalURL}
              renderCodeBlock={renderCodeBlock}
            >
              {body}
            </MessageMarkdown>
          </div>
        ) : (
          <span className="whitespace-pre-wrap">{renderMessageBody(message, body)}</span>
        )}
        {!message.attachment ? (
          <MessageTimeMeta
            message={message}
            translation={translation}
            outgoing={!incoming}
            date={date}
            formatters={formatters}
            muted={incoming || agentNotice || internalNote}
            renderDeliveryState={renderDeliveryState}
            onRetry={failedDraft && onRetryFailedMessage ? () => onRetryFailedMessage(failedDraft) : undefined}
            retryDisabled={messageRetryDisabled}
          />
        ) : null}
      </div>
      {message.localAgentTurns.map((turn) => (
        <LocalAgentTurn key={turn.toolCallId} toolCallId={turn.toolCallId} localAgent={turn.localAgent} conversationId={conversationID} onToggle={onToggleProcess} />
      ))}
    </div>
  )
}
