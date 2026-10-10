/** 消息页当前选中的会话或聊天草稿。 */
import type { InboxConversation, MemberOption } from "@/api"

/** 尚未产生正式会话的聊天草稿。 */
export type ChatDraft =
  | { kind: "direct-draft"; member: MemberOption }
  | { kind: "agent-draft"; member: MemberOption; conversationId: string }

/** 消息页当前选中的会话或草稿。 */
export type ConversationSelection =
  ChatDraft | { kind: "conversation"; conversation: InboxConversation }
