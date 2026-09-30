/** 会话编辑器的输入参数与草稿桥接协议。 */
import type { RefObject } from "react"
import type {
  ConversationType, MessageVisibility, ConversationMessageData, ServiceInboxConversationData,
  ConversationMessageReference, DirectTextMessageInput, GroupParticipant, InboxConversationData, MemberOption,
  ServiceSource,
} from "@/api"
import type { OutgoingConversationDraft } from "@/lib/outgoing-message-store"

/** 读取和替换回复输入框草稿的入口。 */
export type ComposerDraftBridge = {
  read: () => string
  replace: (body: string) => void
}

/** 服务会话来源渠道的附件与输入状态能力。 */
export type CustomerChannelCapabilities = NonNullable<ServiceInboxConversationData["service"]["channel"]>

/** 服务会话对客回复的接收方：发起人名称、联系人编号与来源，渠道来源带渠道名称。 */
export type ServiceRecipient = {
  name: string | null
  contactNumber: number | null
  source: ServiceSource
  channelName: string | null
}

/** 按服务会话摘要返回对客回复的接收方。 */
export function serviceRecipient(service: ServiceInboxConversationData["service"]): ServiceRecipient {
  return { name: service.requesterName, contactNumber: service.requesterContactNumber, source: service.source, channelName: service.channel?.name ?? null }
}

/** 不能回复时显示在原因旁的操作。 */
export type ComposerDisabledAction = { label: string; busy: boolean; onClick: () => void }

/** 会话编辑器的调用参数。 */
export type ConversationComposerProps = {
  attachmentTargetIdentityID?: string
  attachmentAgentDraft?: { conversationID: string; agentIdentityID: string; servedConversationID?: string }
  customerChannel?: CustomerChannelCapabilities | null
  onAttachmentConversationCreated?: (conversation: InboxConversationData | null, conversationID: string) => void
  draftBridgeRef?: RefObject<ComposerDraftBridge | null>
  conversationID: string
  conversationType: ConversationType
  /** 处理方查看服务会话：回复与内部备注走服务会话发送。 */
  service?: boolean
  /** 处理方查看服务会话时对客回复的接收方，输入区上方写明回复发往的对象。 */
  serviceRecipient?: ServiceRecipient | null
  submitOnEnter?: boolean
  refocusAfterSubmit?: boolean
  disabledReason?: string | null
  disabledAction?: ComposerDisabledAction | null
  visibility?: MessageVisibility
  onVisibilityChange?: (visibility: MessageVisibility) => void
  replyTo?: ConversationMessageReference | null
  groupParticipants?: GroupParticipant[]
  noteMentionMembers?: MemberOption[]
  currentIdentityID?: string
  onReplyToChange?: (message: ConversationMessageReference | null) => void
  onBeforeSend?: () => Promise<boolean>
  /** 输入区在此登记按原发送逻辑编号重发失败消息的入口。 */
  resendRef?: RefObject<((draft: OutgoingConversationDraft) => void) | null>
  /** 会话草稿键，未发送的正文与提醒按此键保存和恢复。 */
  draftKey?: string
  onSending: (message: OutgoingConversationDraft) => void
  onSent: (clientMessageID: string, message: ConversationMessageData) => void
  onFailed: (clientMessageID: string) => void
  /** 从时间线移除一条发送项。 */
  onDiscard?: (clientMessageID: string) => void
  onSucceeded: () => void
  sendIndividualMessage?: (
    input: DirectTextMessageInput,
  ) => Promise<ConversationMessageData>
}
