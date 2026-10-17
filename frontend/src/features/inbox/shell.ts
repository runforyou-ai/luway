/**
 * Shell / 工作台窄出口：chrome、侧栏与消息运行时只从此文件取符号，避免经 public 打入整图。
 * 路由页面仍直链模块；mobile 与其它宿主继续用 public。
 */

export { ChatRailSections } from "@/features/inbox/chat-rail"
export { ConversationWindowPage } from "@/features/inbox/conversation-window-page"
export { loadInboxAttention, useInboxAttention } from "@/features/inbox/list/inbox-attention"
export { GlobalSearchProvider } from "@/features/inbox/search/global-search"
export { useGlobalSearch } from "@/features/inbox/state/global-search-context"
export { AttachmentQueueProvider } from "@/features/inbox/state/attachment-queue-context"
export { ComposerDraftProvider } from "@/features/inbox/state/composer-draft-context"
export { OutgoingMessageProvider } from "@/features/inbox/state/outgoing-message-context"
