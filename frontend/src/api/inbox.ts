/** 成员收件箱列表、检索、筛选候选与个人会话设置调用。 */
import { enqueueConversationUnreadChange } from "@/api/conversation-read-queue"
import type {
  AgentInboxConversation,
  ArchivedConversationList,
  ArchivedConversationListInput,
  ConversationPinInput,
  ConversationUnreadMarkInput,
  DirectInboxConversation,
  GroupInboxConversation,
  Inbox,
  InboxConversation,
  InboxQuery,
  InboxSearchInput,
  InboxSearchResult,
  LoadInboxInput,
  ServiceInboxConversation,
} from "@/api/generated/contract"
import {
  type ConversationPinPosition,
  ConversationType,
  InboxPartition,
  InboxScope,
  InboxSearchRange,
} from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

export type ServiceInboxConversationData = InboxConversation & {
  type: typeof ConversationType.Channel | typeof ConversationType.Agent
  service: ServiceInboxConversation
  direct: null
  group: null
  agent: null
}

export type DirectInboxConversationData = InboxConversation & {
  type: typeof ConversationType.Direct
  service: null
  direct: DirectInboxConversation
  group: null
  agent: null
}

export type GroupInboxConversationData = InboxConversation & {
  type: typeof ConversationType.Group
  service: null
  direct: null
  group: GroupInboxConversation
  agent: null
}

export type AgentInboxConversationData = InboxConversation & {
  type: typeof ConversationType.Agent
  service: null
  direct: null
  group: null
  agent: AgentInboxConversation
}

/** 置顶写入命令；未给出位置时新置顶追加到置顶末尾，已置顶保持原位。 */
export type ConversationPinCommand = Pick<
  ConversationPinInput,
  "pinned" | "expectedPinOrderVersion"
> & {
  position?: ConversationPinPosition
  neighborId?: string
}

/** 判断收件箱项是否只带有指定类型的会话载荷。 */
function hasPayload(
  conversation: InboxConversation,
  payload: "service" | "direct" | "group" | "agent",
) {
  return (["service", "direct", "group", "agent"] as const).every(
    (key) => (conversation[key] !== null) === (key === payload),
  )
}

/** 判断统一收件箱项是否为处理方视角的服务会话：渠道会话或承载服务会话的 AI 聊天。 */
export function isServiceInboxConversation(
  conversation: InboxConversation,
): conversation is ServiceInboxConversationData {
  return (
    (conversation.type === ConversationType.Channel ||
      conversation.type === ConversationType.Agent) &&
    hasPayload(conversation, "service")
  )
}

/** 判断统一收件箱项是否为结构完整的内部单聊。 */
export function isDirectInboxConversation(
  conversation: InboxConversation,
): conversation is DirectInboxConversationData {
  return conversation.type === ConversationType.Direct && hasPayload(conversation, "direct")
}

/** 判断统一收件箱项是否为结构完整的企业群聊。 */
export function isGroupInboxConversation(
  conversation: InboxConversation,
): conversation is GroupInboxConversationData {
  return conversation.type === ConversationType.Group && hasPayload(conversation, "group")
}

/** 判断收件箱项是否为独立 AI 聊天。 */
export function isAgentInboxConversation(
  conversation: InboxConversation,
): conversation is AgentInboxConversationData {
  return conversation.type === ConversationType.Agent && hasPayload(conversation, "agent")
}

/** 判断统一收件箱项是否为支持静音和手动未读的内部会话。 */
export function isInternalInboxConversation(
  conversation: InboxConversation,
): conversation is
  | AgentInboxConversationData
  | DirectInboxConversationData
  | GroupInboxConversationData {
  return (
    isAgentInboxConversation(conversation) ||
    isDirectInboxConversation(conversation) ||
    isGroupInboxConversation(conversation)
  )
}

/** 补全列表与检索共用的筛选条件，未给出的筛选按不限处理。 */
function inboxFilters(query: Partial<InboxQuery>) {
  return {
    pendingKind: query.pendingKind,
    queueFilter: query.queueFilter,
    queueTeamId: query.queueTeamId ?? "",
    channelId: query.channelId ?? "",
    source: query.source,
    audience: query.audience,
    serviceStatus: query.serviceStatus,
    assigneeFilter: query.assigneeFilter,
    assigneeIdentityId: query.assigneeIdentityId ?? "",
    kinds: query.kinds ?? [],
  }
}

/** 读取成员会话列表，未指定范围时读取待处理服务会话。 */
export function loadInbox(query: Partial<LoadInboxInput> = {}): Promise<Inbox> {
  return ops.loadInbox({
    ...inboxFilters(query),
    partition: query.partition ?? InboxPartition.All,
    scope: query.scope ?? InboxScope.Pending,
    search: query.search ?? "",
    searchRange: query.searchRange ?? InboxSearchRange.List,
    cursor: query.cursor ?? "",
    beforeCursor: query.beforeCursor ?? "",
    limit: query.limit ?? 50,
  })
}

/** 按最近活动倒序分页读取本人已归档的群聊、单聊与 AI 聊天，未指定类型时不限类型。 */
export function listArchivedConversations(
  input: Partial<ArchivedConversationListInput>,
  signal?: AbortSignal,
): Promise<ArchivedConversationList> {
  return ops.listArchivedConversations({
    kind: input.kind,
    search: input.search ?? "",
    page: input.page ?? 1,
    pageSize: input.pageSize ?? 50,
  }, signal)
}

/** 按范围检索会话名称、消息和人员，每组最多六条；列表筛选只在列表范围生效。 */
export function searchInbox(input: Partial<InboxSearchInput>, signal?: AbortSignal): Promise<InboxSearchResult> {
  return ops.searchInbox({
    ...inboxFilters(input),
    query: input.query ?? "",
    range: input.range ?? InboxSearchRange.Readable,
    conversationId: input.conversationId ?? "",
    scope: input.scope,
  }, signal)
}

/** 读取会话原位置附近的列表窗口及当前资格。 */
export const getInboxContext = ops.getInboxContext

/** 重读已加载首尾边界之间的完整列表范围。 */
export const readInboxWindow = ops.readInboxWindow

/** 批量核对指定会话的阅读和列表资格。 */
export const readInboxConversations = ops.readInboxConversations

/** 独立读取当前用户可见的会话摘要。 */
export const getInboxConversation = ops.getInboxConversation

/** 读取有效真人和 AI 客服筛选项。 */
export async function listServiceAssignees() {
  const output = await ops.listServiceAssignees()
  return output.assignees
}

/** 读取可作为客服队列的团队，本人所在团队排在前面。 */
export async function listServiceQueueTeams() {
  const output = await ops.listServiceQueueTeams()
  return output.teams
}

/** 读取渠道筛选候选，含已停用渠道。 */
export async function listInboxChannels() {
  const output = await ops.listInboxChannels()
  return output.channels
}

/** 保存当前用户的会话置顶事实与置顶顺序。 */
export function updateConversationPin(conversationID: string, command: ConversationPinCommand) {
  return ops.updateConversationPin(conversationID, {
    pinned: command.pinned,
    position: command.position,
    neighborId: command.neighborId ?? "",
    expectedPinOrderVersion: command.expectedPinOrderVersion,
  })
}

/** 保存独立于阅读水位的个人未读标记。 */
export function updateConversationUnreadMark(conversationID: string, input: ConversationUnreadMarkInput) {
  return enqueueConversationUnreadChange(conversationID, () =>
    ops.updateConversationUnreadMark(conversationID, input),
  )
}

/** 保存当前用户对群聊、单聊或 AI 聊天的归档状态，归档同时取消置顶。 */
export const updateConversationArchive = ops.updateConversationArchive

/** 保存当前用户的原生会话提醒设置。 */
export const updateConversationNotificationSettings = ops.updateConversationNotificationSettings

/** 上报当前用户的输入状态：单聊与群聊发给其他真人成员，网站渠道客户会话发给该线程访客，AI 聊天服务会话发给企业成员发起人。 */
export function reportConversationTyping(conversationID: string, active: boolean) {
  return ops.reportConversationTyping(conversationID, { active })
}
