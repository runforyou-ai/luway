/** 验证失权清理真实查询缓存，并阻止迟到响应恢复正文。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { stripTypeScriptTypes } from "node:module"
import { runInNewContext } from "node:vm"
import { test } from "node:test"
import { QueryClient, QueryObserver, isCancelledError } from "@tanstack/react-query"
import { resourceKeys } from "../src/hooks/resource-keys.ts"

const source = readFileSync(new URL("../src/features/inbox/conversation-resources.ts", import.meta.url), "utf8")
const exports: { clearConversationResources?: (client: QueryClient, id: string) => void } = {}
runInNewContext(stripTypeScriptTypes(source).replace(/import[^\n]+\n/g, "").replace("export function", "function") + "\nexports.clearConversationResources = clearConversationResources", { exports, resourceKeys })
const clearConversationResources = exports.clearConversationResources!

// 验证同一 ID 的正文、引用、附件和列表旧响应一起失效，其他会话正文不受影响。
test("失权清理移除衍生缓存，迟到响应不会恢复旧正文或旧列表", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  try {
    const id = "removed"
    client.setQueryData(resourceKeys.conversationSummary(id), null)
    for (const key of [
      resourceKeys.conversationMessages(id),
      resourceKeys.conversationMessageContext(id, "reply"),
      resourceKeys.groupConversation(id),
      resourceKeys.attachmentDownload(id, "attachment"),
      resourceKeys.directConversation("peer"),
      resourceKeys.inboxConversations({ ids: [id] }),
    ]) client.setQueryData(key, { secret: "旧正文" })
    client.setQueryData(resourceKeys.conversationMessages("another"), { body: "其他会话" })
    const gate = Promise.withResolvers<unknown>()
    const pageKey = resourceKeys.conversationMessagePage(id, { before: "cursor" })
    const pending = client.fetchQuery({ queryKey: pageKey, queryFn: () => gate.promise }).catch((error) => error)
    const summariesGate = Promise.withResolvers<unknown>()
    const summariesKey = resourceKeys.serviceSummaries(id)
    const pendingSummaries = client.fetchQuery({ queryKey: summariesKey, queryFn: () => summariesGate.promise }).catch((error) => error)
    const listGate = Promise.withResolvers<unknown>()
    const listKey = resourceKeys.inbox({ scope: "internal" })
    const pendingList = client.fetchQuery({ queryKey: listKey, queryFn: () => listGate.promise }).catch((error) => error)
    clearConversationResources(client, id)
    gate.resolve({ messages: ["旧正文"] })
    summariesGate.resolve({ summaries: [{ body: "旧小结正文" }] })
    listGate.resolve({ conversations: [{ id, preview: "旧正文" }] })
    assert.ok(isCancelledError(await pending))
    assert.ok(isCancelledError(await pendingSummaries))
    assert.ok(isCancelledError(await pendingList))
    assert.equal(client.getQueryData(pageKey), undefined)
    assert.equal(client.getQueryData(summariesKey), undefined)
    assert.equal(client.getQueryData(listKey), undefined)
    assert.equal(client.getQueryData(resourceKeys.conversationMessages(id)), undefined)
    assert.equal(client.getQueryData(resourceKeys.conversationSummary(id)), null)
    assert.deepEqual(client.getQueryData(resourceKeys.conversationMessages("another")), { body: "其他会话" })
    assert.equal(client.getQueryData(resourceKeys.attachmentDownload(id, "attachment")), undefined)
    assert.equal(client.getQueryData(resourceKeys.inboxConversations({ ids: [id] })), undefined)
  } finally { client.clear() }
})

// 验证正在展示的列表立即丢弃失权摘要，并通过权威请求恢复其余内容。
test("活跃列表清除敏感摘要后重新读取，不保留旧数据快照", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  const key = resourceKeys.inbox({ scope: "internal" })
  client.setQueryData(key, ["removed", "other"])
  const gate = Promise.withResolvers<string[]>()
  const observer = new QueryObserver(client, { queryKey: key, queryFn: () => gate.promise, staleTime: Infinity })
  const seen: unknown[] = []
  const unsubscribe = observer.subscribe((result) => seen.push(result.data))
  try {
    clearConversationResources(client, "removed")
    assert.equal(observer.getCurrentResult().data, undefined)
    gate.resolve(["other"])
    await client.fetchQuery({ queryKey: key, queryFn: () => gate.promise })
    assert.deepEqual(observer.getCurrentResult().data, ["other"])
    assert.ok(seen.includes(undefined))
  } finally { unsubscribe(); client.clear() }
})

test("窗口批次失权重读期间保留其余摘要，已确认安全的新批次不被重复清理", () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  const oldKey = resourceKeys.inboxConversations({ conversationIds: ["removed", "other"] })
  const safeKey = resourceKeys.inboxConversations({ conversationIds: ["other"] })
  client.setQueryData(oldKey, { results: [{ id: "removed", conversation: { id: "removed" } }, { id: "other", conversation: { id: "other" } }] })
  const observer = new QueryObserver(client, { queryKey: oldKey, enabled: false })
  const unsubscribe = observer.subscribe(() => {})
  try {
    clearConversationResources(client, "removed")
    assert.ok(observer.getCurrentResult().data)
    client.setQueryData(safeKey, { results: [{ id: "other", conversation: { id: "other" } }] })
    unsubscribe()
    clearConversationResources(client, "removed")
    assert.equal(client.getQueryData(oldKey), undefined)
    assert.ok(client.getQueryData(safeKey))
  } finally { unsubscribe(); client.clear() }
})
