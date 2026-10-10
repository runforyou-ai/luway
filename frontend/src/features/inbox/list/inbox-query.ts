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
import { parseEnum } from "@/lib/enum"

/** 收件箱页签的展示顺序。 */
export const inboxTabs = [
  { value: InboxScope.Pending, label: "tabPending" },
  { value: InboxScope.All, label: "tabAll" },
] as const

/** 收件箱页签对应的范围。 */
export type InboxTab = (typeof inboxTabs)[number]["value"]

/** 待处理条目类型筛选项。 */
export const inboxPendingKindOptions = [
  { value: InboxPendingKind.Reply, label: "pendingKindReply" },
  { value: InboxPendingKind.Mention, label: "pendingKindMention" },
  { value: InboxPendingKind.Queue, label: "pendingKindQueue" },
] as const

/** 服务对象筛选项；available 为假的服务对象尚未接入，只展示不可选。 */
export const serviceAudienceOptions = [
  { value: ServiceAudience.Customer, label: "audienceCustomer", available: true },
  { value: ServiceAudience.Employee, label: "audienceEmployee", available: true },
  { value: ServiceAudience.Partner, label: "audiencePartner", available: false },
] as const

/** 聊天范围的会话类型筛选项，顺序同时决定地址参数和游标的规范化顺序。 */
export const chatKindOptions = [
  { kind: ConversationType.Direct, label: "filterKindDirect" },
  { kind: ConversationType.Group, label: "filterKindGroup" },
  { kind: ConversationType.Agent, label: "filterKindAgent" },
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

/** 带列表范围的规范化筛选，列表页与页签使用。 */
export type ScopedInboxQuery = NormalizedInboxQuery & { scope: InboxScope }

/** 规范化前的列表筛选，未指定的条件取默认值，未指定范围表示全部可读会话。 */
export type InboxQueryInput = Partial<InboxQuery>

/** 把队列筛选编码为单值：空为全部队列，public 为公共队列，其余为团队编号。 */
export function inboxQueueParam(query: Pick<InboxQuery, "queueFilter" | "queueTeamId">) {
  if (query.queueFilter === ServiceQueueFilter.Public) {
    return ServiceQueueFilter.Public
  }
  return query.queueFilter === ServiceQueueFilter.Team
    ? query.queueTeamId
    : ""
}

/** 把队列单值解码为队列筛选和团队编号。 */
export function inboxQueueFromParam(value: string) {
  if (value === ServiceQueueFilter.Public) {
    return { queueFilter: ServiceQueueFilter.Public, queueTeamId: "" }
  }
  return value
    ? { queueFilter: ServiceQueueFilter.Team, queueTeamId: value }
    : { queueFilter: ServiceQueueFilter.All, queueTeamId: "" }
}

/** 把负责人筛选编码为单值：空为不限，unassigned 为未分配，其余为企业身份编号。 */
export function inboxAssigneeParam(query: Pick<InboxQuery, "assigneeFilter" | "assigneeIdentityId">) {
  if (query.assigneeFilter === InboxAssigneeFilter.Unassigned) {
    return InboxAssigneeFilter.Unassigned
  }
  return query.assigneeFilter === InboxAssigneeFilter.Identity
    ? query.assigneeIdentityId
    : ""
}

/** 把负责人单值解码为负责人筛选和企业身份编号。 */
export function inboxAssigneeFromParam(value: string) {
  if (value === InboxAssigneeFilter.Unassigned) {
    return { assigneeFilter: InboxAssigneeFilter.Unassigned, assigneeIdentityId: "" }
  }
  return value
    ? { assigneeFilter: InboxAssigneeFilter.Identity, assigneeIdentityId: value }
    : { assigneeFilter: InboxAssigneeFilter.All, assigneeIdentityId: "" }
}

/** 按范围规范化筛选，范围外条件取空值，与服务端的范围校验一致。 */
export function normalizeInboxQuery<Query extends InboxQueryInput>(query: Query): NormalizedInboxQuery & Pick<Query, "scope"> {
  const pending = query.scope === InboxScope.Pending
  const all = query.scope === InboxScope.All
  const service = pending || all
  const pendingKind = pending ? (query.pendingKind) : undefined
  // 队列筛选只在待领取类型生效。
  const queue =
    pendingKind === InboxPendingKind.Queue
      ? inboxQueueFromParam(inboxQueueParam({ queueFilter: query.queueFilter, queueTeamId: query.queueTeamId ?? "" }))
      : { queueFilter: undefined, queueTeamId: "" }
  const assignee = all
    ? inboxAssigneeFromParam(inboxAssigneeParam({ assigneeFilter: query.assigneeFilter, assigneeIdentityId: query.assigneeIdentityId ?? "" }))
    : { assigneeFilter: undefined, assigneeIdentityId: "" }
  return {
    // 待处理按等待起点排序，不区分置顶分区；其余调用方显式指定分区，未指定时读取完整排序。
    partition: pending ? InboxPartition.All : (query.partition ?? InboxPartition.All),
    scope: query.scope,
    pendingKind,
    ...queue,
    channelId: service ? (query.channelId ?? "") : "",
    // 按渠道筛选时只携带渠道编号，来源为空。
    source: service && !query.channelId ? (query.source) : undefined,
    audience: service ? (query.audience) : undefined,
    serviceStatus: all
      ? query.serviceStatus === ServiceSessionStatus.Closed
        ? ServiceSessionStatus.Closed
        : ServiceSessionStatus.Open
      : undefined,
    ...assignee,
    kinds: query.scope === InboxScope.Chat ? normalizeChatKinds(query.kinds ?? []) : [],
    search: query.search ?? "",
    searchRange: query.searchRange ?? InboxSearchRange.List,
  }
}

/** 不带列表范围与筛选的查询，按编号读取时只核对阅读资格，搜索时覆盖全部可读会话。 */
export const readableInboxQuery: NormalizedInboxQuery = {
  ...normalizeInboxQuery({ scope: InboxScope.Chat }),
  scope: undefined,
}

/** 从地址参数解析列表筛选，未指定或不在可选范围内的页签取第一个可选范围。 */
export function inboxQueryFromSearch(
  params: URLSearchParams,
  scopes: readonly InboxScope[] = inboxTabs.map((tab) => tab.value),
): ScopedInboxQuery {
  const scope = parseEnum(InboxScope, params.get("tab"))
  return normalizeInboxQuery({
    scope: scope && scopes.includes(scope) ? scope : scopes[0],
    pendingKind: parseEnum(InboxPendingKind, params.get("kind")),
    ...inboxQueueFromParam(params.get("queue") ?? ""),
    channelId: params.get("channel") ?? "",
    source: parseEnum(ServiceSource, params.get("source")),
    audience: parseEnum(ServiceAudience, params.get("audience")),
    serviceStatus:
      params.get("status") === ServiceSessionStatus.Closed
        ? ServiceSessionStatus.Closed
        : ServiceSessionStatus.Open,
    ...inboxAssigneeFromParam(params.get("assignee") ?? ""),
    kinds: (params.get("kinds") ?? "").split(",") as ConversationType[],
  })
}

/** 把已规范化的筛选写回地址参数，取默认值的条件不写入。 */
export function writeInboxQuerySearch(
  params: URLSearchParams,
  query: NormalizedInboxQuery,
) {
  const write = (name: string, value: string | undefined) =>
    value ? params.set(name, value) : params.delete(name)
  write("tab", query.scope)
  write("kind", query.pendingKind)
  write("queue", inboxQueueParam(query))
  write("channel", query.channelId)
  write("source", query.source)
  write("audience", query.audience)
  write(
    "status",
    query.serviceStatus === ServiceSessionStatus.Closed
      ? query.serviceStatus
      : "",
  )
  write("assignee", inboxAssigneeParam(query))
  write("kinds", query.kinds.join(","))
}
