/**
 * Inbox 对外入口：workspace、mobile、shell 等宿主只从本文件取符号，不直连内部路径。
 * 按用途分组；新增对外能力时在此登记。
 */

// —— 路由与独立窗口 ——
export { ChatRoute } from "@/features/inbox/chat-route"
export { InboxRoute } from "@/features/inbox/inbox-route"
export { ConversationWindowPage } from "@/features/inbox/conversation-window-page"
export { ArchivedChatsPage } from "@/features/inbox/archived-chats-page"
export { ChatRailSections } from "@/features/inbox/chat-rail"

// —— 列表与查询 ——
export {
  chatKindOptions,
  inboxAssigneeFromParam,
  inboxAssigneeParam,
  inboxPendingKindOptions,
  inboxQueryFromSearch,
  inboxQueueFromParam,
  inboxQueueParam,
  inboxTabs,
  normalizeInboxQuery,
  readableInboxQuery,
  serviceAudienceOptions,
  toggleChatKinds,
  writeInboxQuerySearch,
  type InboxTab,
  type NormalizedInboxQuery,
  type ScopedInboxQuery,
} from "@/features/inbox/list/inbox-query"
export { conversationPreview } from "@/features/inbox/list/conversation-preview"
export { loadInboxAttention, useInboxAttention } from "@/features/inbox/list/inbox-attention"
export { ConversationListMenu, useConversationListActions } from "@/features/inbox/list/conversation-list-menu"
export { ConversationRowContent } from "@/features/inbox/list/conversation-row-content"
export { InboxListPanel } from "@/features/inbox/list/inbox-list-panel"
export type { InboxListBookmark } from "@/features/inbox/list/inbox-list-controller"
export {
  useInboxList,
  usePartitionedInboxList,
  type InboxList,
  type InboxListViewport,
  type PartitionedInboxList,
} from "@/features/inbox/list/use-inbox-list"
export { useInboxListViewport } from "@/features/inbox/list/use-inbox-list-viewport"
export { useConversationArchive } from "@/features/inbox/list/use-conversation-archive"
export {
  PinnedConversationList,
  pinMoveCommand,
  pinSortableStyle,
  usePinSortable,
  type PinSortable,
} from "@/features/inbox/list/pinned-sort"

// —— 搜索 ——
export { GlobalSearchProvider } from "@/features/inbox/search/global-search"
export { useGlobalSearch } from "@/features/inbox/state/global-search-context"
export { InboxSearchConversationList } from "@/features/inbox/search/inbox-search-conversation-list"
export { highlightName, InboxSearchExcerpt } from "@/features/inbox/search/inbox-search-panel"
export {
  useInboxSearchResults,
  type InboxSearchPersonData,
  type InboxSearchType,
} from "@/features/inbox/search/use-inbox-search"
export { useSearchQueries } from "@/features/inbox/search/use-search-queries"

// —— 会话详情与草稿 ——
export { DirectConversationDraftAvatar } from "@/features/inbox/conversation/direct-conversation-draft-header"
export { InboxConversationTarget } from "@/features/inbox/conversation/inbox-conversation-target"
export { InternalConversationDetails } from "@/features/inbox/conversation/internal-conversation-details"
export { useAccountDisabledReason } from "@/features/inbox/conversation/use-account-disabled-reason"
export { useConversationReadMarker } from "@/features/inbox/conversation/use-conversation-read-marker"
export { useFirstChatMessage } from "@/features/inbox/conversation/use-first-chat-message"

// —— 编辑器 ——
export { ConversationComposer } from "@/features/inbox/composer/conversation-composer"
export {
  serviceRecipient,
  type ComposerDisabledAction,
  type ComposerDraftBridge,
  type CustomerChannelCapabilities,
  type ServiceRecipient,
} from "@/features/inbox/composer/conversation-composer-types"
export { useThreadComposerBridge } from "@/features/inbox/composer/use-thread-composer-bridge"

// —— 时间线 ——
export {
  ConversationTimeline,
  type ConversationLocateTarget,
} from "@/features/inbox/timeline/conversation-timeline"

// —— 群聊 ——
export { GroupAvatar } from "@/features/inbox/group/group-avatar"
export {
  createGroupConversationSchema,
  groupAdditionalMemberMaxCount,
  groupDescriptionMaxLength,
  groupMemberMaxCount,
  groupProfileSchema,
  groupTitleMaxLength,
} from "@/features/inbox/group/group-conversation-schema"
export { GroupDissolveDialog } from "@/features/inbox/group/group-dissolve-dialog"
export { GroupMemberOptionContent } from "@/features/inbox/group/group-member-option-content"
export { useGroupImageUpload } from "@/features/inbox/group/use-group-image-upload"
export { useGroupMute } from "@/features/inbox/group/use-group-mute"

// —— 客户服务 ——
export { ServiceBusinessQueries } from "@/features/inbox/service/customer-business-queries"
export {
  CopilotAgentSelect,
  CopilotApplyReplyDialog,
  CopilotThreadsLoadState,
  CopilotThreadView,
  useCopilotThreadMeta,
  useServiceCopilot,
  type ServiceCopilot,
} from "@/features/inbox/service/customer-copilot-panel"
export { useCustomerLanguage } from "@/features/inbox/service/customer-language-menu"
export { CustomerProfileDetails } from "@/features/inbox/service/customer-profile-details"
export { CustomerServiceHistory } from "@/features/inbox/service/customer-service-history"
export {
  CustomerSessionCloseDialog,
  customerReplyBlocker,
  customerReplyDisabledReason,
  useCustomerSessionActions,
  useReplyWindowNow,
  type CustomerSessionActions,
} from "@/features/inbox/service/customer-session-actions"
export { HandoffSummaryCard } from "@/features/inbox/service/handoff-summary-card"

// —— 共享展示与状态 ——
export {
  AgentRunBlocks,
  AgentTool,
  isPlanToolBlock,
  isUnsettledToolCall,
  localAgentToolName,
  useToolStatusLabel,
} from "@/features/inbox/shared/agent-run-blocks"
export { personalAgentUnavailableLabel } from "@/features/inbox/shared/agent-run-status"
export { ServiceTranscript } from "@/features/inbox/shared/service-transcript"
export { ConversationAvatar } from "@/features/inbox/shared/conversation-avatar"
export { ConversationFilesPanel } from "@/features/inbox/shared/conversation-files-panel"
export { clearConversationResources } from "@/features/inbox/shared/conversation-resources"
export { CustomerTranslationProvider } from "@/features/inbox/shared/customer-translation"
export {
  filterChatTargets,
  listChatTargets,
  orderChatTargets,
} from "@/features/inbox/shared/list-all-member-options"
export { useConversationSummary } from "@/features/inbox/shared/use-conversation-summary"
export { useConversationTime } from "@/features/inbox/shared/use-conversation-time"
export {
  customerTypingSenderName,
  groupTypingSenderName,
  useConversationTypingLabel,
} from "@/features/inbox/shared/use-conversation-typing"
export { useRecentConversations } from "@/features/inbox/shared/use-recent-conversations"

// —— 消息运行时 Provider（由 shell 的 InboxRuntimeProviders 装配） ——
export { AttachmentQueueProvider, useAttachmentQueue } from "@/features/inbox/state/attachment-queue-context"
export { ComposerDraftProvider } from "@/features/inbox/state/composer-draft-context"
export { OutgoingMessageProvider, useOutgoingMessageStore } from "@/features/inbox/state/outgoing-message-context"
