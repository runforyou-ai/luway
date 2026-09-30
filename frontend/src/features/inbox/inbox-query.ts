/** 会话列表范围、筛选规则与地址参数读写。 */
import {
  ConversationType,
  ServiceQueueFilter,
  InboxAssigneeFilter,
  InboxPartition,
  InboxPendingKind,
  InboxScope,
  InboxSearchRange,
  ServiceAudience,
  ServiceSessionStatus,
  ServiceSource,
  type InboxQuery,
} from "@/api"
import { optionalWailsEnum } from "@/lib/wails-enum"

/** 收件箱页签的展示顺序。 */
export const inboxTabs = [
  { value: InboxScope.InboxScopePending, label: "tabPending" },
  { value: InboxScope.InboxScopeAll, label: "tabAll" },
] as const

/** 收件箱页签对应的范围。 */
export type InboxTab = (typeof inboxTabs)[number]["value"]

/** 待处理条目类型筛选项。 */
export const inboxPendingKindOptions = [
  { value: InboxPendingKind.InboxPendingKindReply, label: "pendingKindReply" },
  { value: InboxPendingKind.InboxPendingKindMention, label: "pendingKindMention" },
  { value: InboxPendingKind.InboxPendingKindQueue, label: "pendingKindQueue" },
] as const

/** 服务对象筛选项；available 为假的服务对象尚未接入，只展示不可选。 */
export const serviceAudienceOptions = [
  { value: ServiceAudience.ServiceAudienceCustomer, label: "audienceCustomer", available: true },
  { value: ServiceAudience.ServiceAudienceEmployee, label: "audienceEmployee", available: true },
  { value: ServiceAudience.ServiceAudiencePartner, label: "audiencePartner", available: false },
] as const

/** 聊天范围的会话类型筛选项，顺序同时决定地址参数和游标的规范化顺序。 */
export const chatKindOptions = [
  { kind: ConversationType.ConversationTypeDirect, label: "filterKindDirect" },
  { kind: ConversationType.ConversationTypeGroup, label: "filterKindGroup" },
  { kind: ConversationType.ConversationTypeAgent, label: "filterKindAgent" },
] as const

/** 规范化聊天范围的会话类型筛选，丢弃范围外类型，覆盖全部类型收敛为空选择。 */
function normalizeChatKinds(kinds: readonly ConversationType[]): ConversationType[] {
  const available = chatKindOptions.map((option) => option.kind)
  const selected = available.filter((kind) => kinds.includes(kind))
  return selected.length === available.length ? [] : selected
}

/** 切换一个会话类型的选中状态。 */
export function toggleChatKinds(
  kinds: readonly ConversationType[],
  kind: ConversationType,
  checked: boolean,
): ConversationType[] {
  return normalizeChatKinds(checked ? [...kinds, kind] : kinds.filter((item) => item !== kind))
}

/** 已按范围规范化的列表筛选，会话类型一律为数组。 */
export type NormalizedInboxQuery = InboxQuery & { kinds: ConversationType[] }

/** 规范化前的列表筛选，未指定的条件取默认值。 */
export type InboxQueryInput = Partial<InboxQuery> & { scope: InboxScope }

/** 把队列筛选编码为单值：空为全部队列，public 为公共队列，其余为团队编号。 */
export function inboxQueueParam(query: Pick<InboxQuery, "queueFilter" | "queueTeamId">) {
  if (query.queueFilter === ServiceQueueFilter.ServiceQueueFilterPublic) {
    return ServiceQueueFilter.ServiceQueueFilterPublic
  }
  return query.queueFilter === ServiceQueueFilter.ServiceQueueFilterTeam
    ? query.queueTeamId
    : ""
}

/** 把队列单值解码为队列筛选和团队编号。 */
export function inboxQueueFromParam(value: string) {
  if (value === ServiceQueueFilter.ServiceQueueFilterPublic) {
    return { queueFilter: ServiceQueueFilter.ServiceQueueFilterPublic, queueTeamId: "" }
  }
  return value
    ? { queueFilter: ServiceQueueFilter.ServiceQueueFilterTeam, queueTeamId: value }
    : { queueFilter: ServiceQueueFilter.ServiceQueueFilterAll, queueTeamId: "" }
}

/** 把负责人筛选编码为单值：空为不限，unassigned 为未分配，其余为企业身份编号。 */
export function inboxAssigneeParam(query: Pick<InboxQuery, "assigneeFilter" | "assigneeIdentityId">) {
  if (query.assigneeFilter === InboxAssigneeFilter.InboxAssigneeFilterUnassigned) {
    return InboxAssigneeFilter.InboxAssigneeFilterUnassigned
  }
  return query.assigneeFilter === InboxAssigneeFilter.InboxAssigneeFilterIdentity
    ? query.assigneeIdentityId
    : ""
}

/** 把负责人单值解码为负责人筛选和企业身份编号。 */
export function inboxAssigneeFromParam(value: string) {
  if (value === InboxAssigneeFilter.InboxAssigneeFilterUnassigned) {
    return { assigneeFilter: InboxAssigneeFilter.InboxAssigneeFilterUnassigned, assigneeIdentityId: "" }
  }
  return value
    ? { assigneeFilter: InboxAssigneeFilter.InboxAssigneeFilterIdentity, assigneeIdentityId: value }
    : { assigneeFilter: InboxAssigneeFilter.InboxAssigneeFilterAll, assigneeIdentityId: "" }
}

/** 按范围规范化筛选，范围外条件取空值，与服务端的范围校验一致。 */
export function normalizeInboxQuery(query: InboxQueryInput): NormalizedInboxQuery {
  const pending = query.scope === InboxScope.InboxScopePending
  const all = query.scope === InboxScope.InboxScopeAll
  const service = pending || all
  const pendingKind = pending ? (query.pendingKind ?? InboxPendingKind.$zero) : InboxPendingKind.$zero
  // 队列筛选只在待领取类型生效。
  const queue =
    pendingKind === InboxPendingKind.InboxPendingKindQueue
      ? inboxQueueFromParam(inboxQueueParam({ queueFilter: query.queueFilter ?? ServiceQueueFilter.$zero, queueTeamId: query.queueTeamId ?? "" }))
      : { queueFilter: ServiceQueueFilter.$zero, queueTeamId: "" }
  const assignee = all
    ? inboxAssigneeFromParam(inboxAssigneeParam({ assigneeFilter: query.assigneeFilter ?? InboxAssigneeFilter.$zero, assigneeIdentityId: query.assigneeIdentityId ?? "" }))
    : { assigneeFilter: InboxAssigneeFilter.$zero, assigneeIdentityId: "" }
  return {
    // 待处理按等待起点排序，不区分置顶分区；其余调用方显式指定分区，未指定时读取完整排序。
    partition: pending ? InboxPartition.InboxPartitionAll : (query.partition ?? InboxPartition.InboxPartitionAll),
    scope: query.scope,
    pendingKind,
    ...queue,
    channelId: service ? (query.channelId ?? "") : "",
    // 按渠道筛选时只携带渠道编号，来源为空。
    source: service && !query.channelId ? (query.source ?? ServiceSource.$zero) : ServiceSource.$zero,
    audience: service ? (query.audience ?? ServiceAudience.$zero) : ServiceAudience.$zero,
    serviceStatus: all
      ? query.serviceStatus === ServiceSessionStatus.ServiceSessionStatusClosed
        ? ServiceSessionStatus.ServiceSessionStatusClosed
        : ServiceSessionStatus.ServiceSessionStatusOpen
      : ServiceSessionStatus.$zero,
    ...assignee,
    kinds: query.scope === InboxScope.InboxScopeChat ? normalizeChatKinds(query.kinds ?? []) : [],
    search: query.search ?? "",
    searchRange: query.searchRange ?? InboxSearchRange.InboxSearchRangeList,
  }
}

/** 不带列表范围与筛选的查询，按编号读取时只核对阅读资格，搜索时覆盖全部可读会话。 */
export const readableInboxQuery: NormalizedInboxQuery = {
  ...normalizeInboxQuery({ scope: InboxScope.InboxScopeChat }),
  scope: InboxScope.$zero,
}

/** 从地址参数解析列表筛选，未指定或不在可选范围内的页签取第一个可选范围。 */
export function inboxQueryFromSearch(
  params: URLSearchParams,
  scopes: readonly InboxScope[] = inboxTabs.map((tab) => tab.value),
): NormalizedInboxQuery {
  const scope = optionalWailsEnum(InboxScope, params.get("tab"))
  return normalizeInboxQuery({
    scope: scope && scopes.includes(scope) ? scope : scopes[0],
    pendingKind: optionalWailsEnum(InboxPendingKind, params.get("kind")) ?? InboxPendingKind.$zero,
    ...inboxQueueFromParam(params.get("queue") ?? ""),
    channelId: params.get("channel") ?? "",
    source: optionalWailsEnum(ServiceSource, params.get("source")) ?? ServiceSource.$zero,
    audience: optionalWailsEnum(ServiceAudience, params.get("audience")) ?? ServiceAudience.$zero,
    serviceStatus:
      params.get("status") === ServiceSessionStatus.ServiceSessionStatusClosed
        ? ServiceSessionStatus.ServiceSessionStatusClosed
        : ServiceSessionStatus.ServiceSessionStatusOpen,
    ...inboxAssigneeFromParam(params.get("assignee") ?? ""),
    kinds: (params.get("kinds") ?? "").split(",") as ConversationType[],
  })
}

/** 把已规范化的筛选写回地址参数，取默认值的条件不写入。 */
export function writeInboxQuerySearch(
  params: URLSearchParams,
  query: NormalizedInboxQuery,
) {
  const write = (name: string, value: string) =>
    value ? params.set(name, value) : params.delete(name)
  write("tab", query.scope)
  write("kind", query.pendingKind)
  write("queue", inboxQueueParam(query))
  write("channel", query.channelId)
  write("source", query.source)
  write("audience", query.audience)
  write(
    "status",
    query.serviceStatus === ServiceSessionStatus.ServiceSessionStatusClosed
      ? query.serviceStatus
      : "",
  )
  write("assignee", inboxAssigneeParam(query))
  write("kinds", query.kinds.join(","))
}
