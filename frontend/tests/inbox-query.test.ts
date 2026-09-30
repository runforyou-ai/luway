/** 验证会话列表范围的筛选规则与地址参数往返。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { stripTypeScriptTypes } from "node:module"
import { runInNewContext } from "node:vm"
import { test } from "node:test"

const ConversationType = {
  $zero: "",
  ConversationTypeChannel: "channel",
  ConversationTypeDirect: "direct",
  ConversationTypeGroup: "group",
  ConversationTypeAgent: "agent",
}
const InboxScope = {
  $zero: "",
  InboxScopePending: "pending",
  InboxScopeAll: "all",
  InboxScopeChat: "chat",
}
const InboxPendingKind = {
  $zero: "",
  InboxPendingKindReply: "reply",
  InboxPendingKindQueue: "queue",
  InboxPendingKindMention: "mention",
}
const InboxAssigneeFilter = {
  $zero: "",
  InboxAssigneeFilterAll: "all",
  InboxAssigneeFilterUnassigned: "unassigned",
  InboxAssigneeFilterIdentity: "identity",
}
const ServiceQueueFilter = {
  $zero: "",
  ServiceQueueFilterAll: "all",
  ServiceQueueFilterPublic: "public",
  ServiceQueueFilterTeam: "team",
}
const ServiceAudience = {
  $zero: "",
  ServiceAudienceCustomer: "customer",
  ServiceAudienceEmployee: "employee",
  ServiceAudiencePartner: "partner",
}
const ServiceSource = {
  $zero: "",
  ServiceSourceChannel: "channel",
  ServiceSourceDirect: "direct",
  ServiceSourceGroup: "group",
}
const ServiceSessionStatus = {
  $zero: "",
  ServiceSessionStatusOpen: "open",
  ServiceSessionStatusClosed: "closed",
}
const InboxPartition = {
  $zero: "",
  InboxPartitionAll: "all",
  InboxPartitionPinned: "pinned",
  InboxPartitionRegular: "regular",
}
const InboxSearchRange = {
  $zero: "",
  InboxSearchRangeList: "list",
  InboxSearchRangeReadable: "readable",
  InboxSearchRangeConversation: "conversation",
}
const optionalWailsEnum = (values: Record<string, string>, value: string | null) =>
  value === null || value === values.$zero || !Object.values(values).includes(value)
    ? undefined
    : value

type InboxQuery = Record<string, unknown> & { scope: string }

const source = readFileSync(new URL("../src/features/inbox/inbox-query.ts", import.meta.url), "utf8")
const module: {
  normalizeInboxQuery?: (query: InboxQuery) => InboxQuery
  inboxQueryFromSearch?: (params: URLSearchParams, scopes?: string[]) => InboxQuery
  writeInboxQuerySearch?: (params: URLSearchParams, query: InboxQuery) => void
  toggleChatKinds?: (kinds: string[], kind: string, checked: boolean) => string[]
} = {}
runInNewContext(
  stripTypeScriptTypes(source).replace(/^import\s[\s\S]*?from "[^"]+"\n/gm, "").replaceAll("export ", "") +
    "\nObject.assign(module, { normalizeInboxQuery, inboxQueryFromSearch, writeInboxQuerySearch, toggleChatKinds })",
  {
    module, ConversationType, InboxScope, InboxPendingKind, InboxAssigneeFilter, InboxPartition, InboxSearchRange,
    ServiceQueueFilter, ServiceAudience, ServiceSource, ServiceSessionStatus, optionalWailsEnum, URLSearchParams,
  },
)
const { normalizeInboxQuery, inboxQueryFromSearch, writeInboxQuerySearch, toggleChatKinds } = module as Required<typeof module>

/** 跨 VM 上下文返回的结果按值比较。 */
function plain<Value>(value: Value): Value {
  return JSON.parse(JSON.stringify(value))
}

/** 各范围都不带条件时的规范化结果。 */
const empty = {
  partition: "all", pendingKind: "", queueFilter: "", queueTeamId: "", channelId: "", source: "", audience: "",
  serviceStatus: "", assigneeFilter: "", assigneeIdentityId: "", kinds: [], search: "", searchRange: "list",
}

test("来源筛选只在服务会话范围且未选渠道时保留", () => {
  assert.deepEqual(plain(normalizeInboxQuery({ scope: "pending", source: "direct" })), { ...empty, scope: "pending", source: "direct" })
  assert.deepEqual(plain(normalizeInboxQuery({ scope: "pending", source: "direct", channelId: "web" })), { ...empty, scope: "pending", channelId: "web" })
  assert.deepEqual(plain(normalizeInboxQuery({ scope: "chat", source: "direct" })), { ...empty, scope: "chat" })
  const params = new URLSearchParams()
  writeInboxQuerySearch(params, normalizeInboxQuery({ scope: "pending", source: "direct" }))
  assert.equal(plain(inboxQueryFromSearch(params, ["pending"])).source, "direct")
})

test("范围外的条件按空值规范化", () => {
  const carried = {
    pendingKind: "queue", queueFilter: "public", channelId: "web", audience: "customer", serviceStatus: "closed",
    assigneeFilter: "identity", assigneeIdentityId: "peer", kinds: ["channel", "group"],
  }
  assert.deepEqual(plain(normalizeInboxQuery({ scope: "chat", ...carried })), { ...empty, scope: "chat", kinds: ["group"] })
  assert.deepEqual(
    plain(normalizeInboxQuery({ scope: "pending", ...carried })),
    { ...empty, scope: "pending", pendingKind: "queue", queueFilter: "public", channelId: "web", audience: "customer" },
  )
  assert.deepEqual(
    plain(normalizeInboxQuery({ scope: "all", ...carried })),
    { ...empty, scope: "all", channelId: "web", audience: "customer", serviceStatus: "closed", assigneeFilter: "identity", assigneeIdentityId: "peer" },
  )
})

test("待处理不区分置顶分区，队列筛选只在待领取类型生效", () => {
  assert.equal(normalizeInboxQuery({ scope: "pending", partition: "pinned" }).partition, "all")
  assert.equal(normalizeInboxQuery({ scope: "all", partition: "pinned" }).partition, "pinned")
  const reply = normalizeInboxQuery({ scope: "pending", pendingKind: "reply", queueFilter: "public" })
  assert.equal(reply.queueFilter, "")
  // 指定队列缺少团队编号时按全部队列处理。
  assert.equal(normalizeInboxQuery({ scope: "pending", pendingKind: "queue", queueFilter: "team", queueTeamId: "" }).queueFilter, "all")
  // 指定负责人缺少身份编号时按不限处理。
  assert.equal(normalizeInboxQuery({ scope: "all", assigneeFilter: "identity", assigneeIdentityId: "" }).assigneeFilter, "all")
})

test("勾满聊天范围全部类型等同不限类型", () => {
  assert.deepEqual(plain(toggleChatKinds(["direct", "group"], "agent", true)), [])
  assert.deepEqual(plain(toggleChatKinds(["direct", "group"], "direct", false)), ["group"])
  assert.deepEqual(plain(toggleChatKinds([], "customer", true)), [])
})

test("地址参数往返保持规范化结果，默认值不写入", () => {
  const original = new URLSearchParams("tab=all&channel=web&audience=customer&status=closed&assignee=peer&conversation=kept")
  const query = inboxQueryFromSearch(original)
  assert.deepEqual(
    plain(query),
    { ...empty, scope: "all", channelId: "web", audience: "customer", serviceStatus: "closed", assigneeFilter: "identity", assigneeIdentityId: "peer" },
  )
  const written = new URLSearchParams(original)
  writeInboxQuerySearch(written, query)
  assert.equal(written.toString(), original.toString())
  // 切到待处理时状态与负责人一起清空，来源与服务对象保留。
  const pending = new URLSearchParams(original)
  writeInboxQuerySearch(pending, normalizeInboxQuery({ ...query, scope: "pending" }))
  assert.equal(pending.toString(), "tab=pending&channel=web&audience=customer&conversation=kept")
  // 未分配写入固定取值，不限是默认值不写入。
  const unassigned = new URLSearchParams()
  writeInboxQuerySearch(unassigned, normalizeInboxQuery({ scope: "all", assigneeFilter: "unassigned" }))
  assert.equal(unassigned.toString(), "tab=all&assignee=unassigned")
  // 置顶分区与会话名称搜索不进入地址参数。
  const pinned = new URLSearchParams(original)
  writeInboxQuerySearch(pinned, normalizeInboxQuery({ ...query, partition: "pinned", search: "周报", searchRange: "readable" }))
  assert.equal(pinned.toString(), original.toString())
})

test("待领取的队列筛选编码为单值", () => {
  const team = "5f3c4a20-6c4e-4c4a-9a3a-1f2b3c4d5e6f"
  const queued = inboxQueryFromSearch(new URLSearchParams(`tab=pending&kind=queue&queue=${team}`))
  assert.deepEqual(plain(queued), { ...empty, scope: "pending", pendingKind: "queue", queueFilter: "team", queueTeamId: team })
  const written = new URLSearchParams()
  writeInboxQuerySearch(written, queued)
  assert.equal(written.toString(), `tab=pending&kind=queue&queue=${team}`)
  const allQueues = new URLSearchParams()
  writeInboxQuerySearch(allQueues, normalizeInboxQuery({ ...queued, queueFilter: "all", queueTeamId: "" }))
  assert.equal(allQueues.toString(), "tab=pending&kind=queue")
})

test("未指定或不可选的页签取第一个可选范围", () => {
  assert.equal(inboxQueryFromSearch(new URLSearchParams("")).scope, "pending")
  assert.equal(inboxQueryFromSearch(new URLSearchParams("tab=chat")).scope, "pending")
  assert.equal(inboxQueryFromSearch(new URLSearchParams(""), ["chat", "pending", "all"]).scope, "chat")
  assert.equal(inboxQueryFromSearch(new URLSearchParams("tab=all"), ["chat", "pending", "all"]).scope, "all")
})
