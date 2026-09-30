/** 成员收件箱列表、检索、筛选候选与个人会话设置调用。 */
import {
  GetInboxContext,
  GetInboxConversation,
  ListArchivedConversations,
  ListInboxChannels,
  ListServiceAssignees,
  ListServiceQueueTeams,
  LoadInbox,
  ReadConversationAttention,
  ReadInboxConversations,
  ReadInboxWindow,
  ReportConversationTyping,
  SearchInbox,
  UpdateConversationArchive,
  UpdateConversationNotificationSettings,
  UpdateConversationPin,
  UpdateConversationUnreadMark,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import type {
  AgentInboxConversation,
  ArchivedConversationList,
  ArchivedConversationListInput,
  ConversationAttention,
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
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import {
  ConversationPinPosition,
  ConversationType,
  InboxAssigneeFilter,
  InboxPartition,
  InboxPendingKind,
  InboxScope,
  InboxSearchRange,
  ServiceAudience,
  ServiceQueueFilter,
  ServiceSessionStatus,
  ServiceSource,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"
import { enqueueConversationUnreadChange } from "@/api/conversation-read-queue"
import type { NonNullArrays } from "@/api/normalize"

type InboxData = NonNullArrays<Inbox>

export type InboxConversationData = NonNullArrays<InboxConversation>

export type ServiceInboxConversationData = InboxConversationData & {
  type: ConversationType.ConversationTypeChannel | ConversationType.ConversationTypeAgent
  service: ServiceInboxConversation
  direct: null
  group: null
  agent: null
}

export type DirectInboxConversationData = InboxConversationData & {
  type: ConversationType.ConversationTypeDirect
  service: null
  direct: DirectInboxConversation
  group: null
  agent: null
}

export type GroupInboxConversationData = InboxConversationData & {
  type: ConversationType.ConversationTypeGroup
  service: null
  direct: null
  group: NonNullArrays<GroupInboxConversation>
  agent: null
}

export type AgentInboxConversationData = InboxConversationData & {
  type: ConversationType.ConversationTypeAgent
  service: null
  direct: null
  group: null
  agent: AgentInboxConversation
}

export type ConversationAttentionData = NonNullArrays<ConversationAttention>

export type InboxSearchResultData = NonNullArrays<InboxSearchResult>

/** 置顶写入命令；未给出位置时新置顶追加到置顶末尾，已置顶保持原位。 */
export type ConversationPinCommand = Pick<
  ConversationPinInput,
  "pinned" | "expectedPinOrderVersion"
> & {
  position?: Exclude<ConversationPinPosition, ConversationPinPosition.$zero>
  neighborId?: string
}

/** 判断收件箱项是否只带有指定类型的会话载荷。 */
function hasPayload(
  conversation: InboxConversationData,
  payload: "service" | "direct" | "group" | "agent",
) {
  return (["service", "direct", "group", "agent"] as const).every(
    (key) => (conversation[key] !== null) === (key === payload),
  )
}

/** 判断统一收件箱项是否为处理方视角的服务会话：渠道会话或承载服务会话的 AI 聊天。 */
export function isServiceInboxConversation(
  conversation: InboxConversationData,
): conversation is ServiceInboxConversationData {
  return (
    (conversation.type === ConversationType.ConversationTypeChannel ||
      conversation.type === ConversationType.ConversationTypeAgent) &&
    hasPayload(conversation, "service")
  )
}

/** 判断统一收件箱项是否为结构完整的内部单聊。 */
export function isDirectInboxConversation(
  conversation: InboxConversationData,
): conversation is DirectInboxConversationData {
  return conversation.type === ConversationType.ConversationTypeDirect && hasPayload(conversation, "direct")
}

/** 判断统一收件箱项是否为结构完整的企业群聊。 */
export function isGroupInboxConversation(
  conversation: InboxConversationData,
): conversation is GroupInboxConversationData {
  return conversation.type === ConversationType.ConversationTypeGroup && hasPayload(conversation, "group")
}

/** 判断收件箱项是否为独立 AI 聊天。 */
export function isAgentInboxConversation(
  conversation: InboxConversationData,
): conversation is AgentInboxConversationData {
  return conversation.type === ConversationType.ConversationTypeAgent && hasPayload(conversation, "agent")
}

/** 判断统一收件箱项是否为支持静音和手动未读的内部会话。 */
export function isInternalInboxConversation(
  conversation: InboxConversationData,
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
    pendingKind: query.pendingKind ?? InboxPendingKind.$zero,
    queueFilter: query.queueFilter ?? ServiceQueueFilter.$zero,
    queueTeamId: query.queueTeamId ?? "",
    channelId: query.channelId ?? "",
    source: query.source ?? ServiceSource.$zero,
    audience: query.audience ?? ServiceAudience.$zero,
    serviceStatus: query.serviceStatus ?? ServiceSessionStatus.$zero,
    assigneeFilter: query.assigneeFilter ?? InboxAssigneeFilter.$zero,
    assigneeIdentityId: query.assigneeIdentityId ?? "",
    kinds: query.kinds ?? [],
  }
}

const loadInboxBound = bind(LoadInbox)

/** 读取成员会话列表，未指定范围时读取待处理服务会话。 */
export function loadInbox(query: Partial<LoadInboxInput> = {}): Promise<InboxData> {
  return loadInboxBound({
    ...inboxFilters(query),
    partition: query.partition ?? InboxPartition.InboxPartitionAll,
    scope: query.scope ?? InboxScope.InboxScopePending,
    search: query.search ?? "",
    searchRange: query.searchRange ?? InboxSearchRange.InboxSearchRangeList,
    cursor: query.cursor ?? "",
    beforeCursor: query.beforeCursor ?? "",
    limit: query.limit ?? 50,
  })
}

const listArchivedConversationsBound = bind(ListArchivedConversations)

/** 按最近活动倒序分页读取本人已归档的群聊、单聊与 AI 聊天，未指定类型时不限类型。 */
export function listArchivedConversations(
  input: Partial<ArchivedConversationListInput>,
  signal?: AbortSignal,
): Promise<NonNullArrays<ArchivedConversationList>> {
  return listArchivedConversationsBound({
    kind: input.kind ?? ConversationType.$zero,
    search: input.search ?? "",
    page: input.page ?? 1,
    pageSize: input.pageSize ?? 50,
  }, signal)
}

const searchInboxBound = bind(SearchInbox)

/** 按范围检索会话名称、消息和人员，每组最多六条；列表筛选只在列表范围生效。 */
export function searchInbox(input: Partial<InboxSearchInput>, signal?: AbortSignal): Promise<InboxSearchResultData> {
  return searchInboxBound({
    ...inboxFilters(input),
    query: input.query ?? "",
    range: input.range ?? InboxSearchRange.InboxSearchRangeReadable,
    conversationId: input.conversationId ?? "",
    scope: input.scope ?? InboxScope.$zero,
  }, signal)
}

/** 读取会话原位置附近的列表窗口及当前资格。 */
export const getInboxContext = bind(GetInboxContext)

/** 重读已加载首尾边界之间的完整列表范围。 */
export const readInboxWindow = bind(ReadInboxWindow)

/** 批量核对指定会话的阅读和列表资格。 */
export const readInboxConversations = bind(ReadInboxConversations)

/** 独立读取当前用户可见的会话摘要。 */
export const getInboxConversation = bind(GetInboxConversation)

/** 读取会话摘要及已知消息之后计入本人提醒的未读消息。 */
export const readConversationAttention = bind(ReadConversationAttention)

const listServiceAssigneesBound = bind(ListServiceAssignees)

/** 读取有效真人和 AI 客服筛选项。 */
export async function listServiceAssignees() {
  const output = await listServiceAssigneesBound()
  return output.assignees
}

const listServiceQueueTeamsBound = bind(ListServiceQueueTeams)

/** 读取可作为客服队列的团队，本人所在团队排在前面。 */
export async function listServiceQueueTeams() {
  const output = await listServiceQueueTeamsBound()
  return output.teams
}

const listInboxChannelsBound = bind(ListInboxChannels)

/** 读取渠道筛选候选，含已停用渠道。 */
export async function listInboxChannels() {
  const output = await listInboxChannelsBound()
  return output.channels
}

const updateConversationPinBound = bind(UpdateConversationPin)

/** 保存当前用户的会话置顶事实与置顶顺序。 */
export function updateConversationPin(conversationID: string, command: ConversationPinCommand) {
  return updateConversationPinBound(conversationID, {
    pinned: command.pinned,
    position: command.position ?? ConversationPinPosition.$zero,
    neighborId: command.neighborId ?? "",
    expectedPinOrderVersion: command.expectedPinOrderVersion,
  })
}

const updateConversationUnreadMarkBound = bind(UpdateConversationUnreadMark)

/** 保存独立于阅读水位的个人未读标记。 */
export function updateConversationUnreadMark(conversationID: string, input: ConversationUnreadMarkInput) {
  return enqueueConversationUnreadChange(conversationID, () =>
    updateConversationUnreadMarkBound(conversationID, input),
  )
}

/** 保存当前用户对群聊、单聊或 AI 聊天的归档状态，归档同时取消置顶。 */
export const updateConversationArchive = bind(UpdateConversationArchive)

/** 保存当前用户的原生会话提醒设置。 */
export const updateConversationNotificationSettings = bind(UpdateConversationNotificationSettings)

const reportConversationTypingBound = bind(ReportConversationTyping)

/** 上报当前用户的输入状态：单聊与群聊发给其他真人成员，网站渠道客户会话发给该线程访客，AI 聊天服务会话发给企业成员发起人。 */
export function reportConversationTyping(conversationID: string, active: boolean) {
  return reportConversationTypingBound(conversationID, { active })
}
