/** 用真实 QueryClient 验证同步重读的串行补读、失败重试与卸载后的停止。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { QueryClient, QueryObserver, onlineManager } from "@tanstack/react-query"
import { ResourceRefresher } from "../src/features/session/resource-refresher.ts"

const key = ["conversation-summary", "c1"]

/** 等待已排队的微任务与 I/O 回调执行完毕。 */
function flush() {
  return new Promise((resolve) => setImmediate(resolve))
}

/** 构造带已有数据与挂载观察者的查询，读取由测试逐次完成。 */
function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } } })
  const reads: PromiseWithResolvers<string>[] = []
  client.setQueryData(key, "v0")
  const observer = new QueryObserver(client, {
    queryKey: key,
    queryFn: () => {
      const read = Promise.withResolvers<string>()
      reads.push(read)
      return read.promise
    },
  })
  const unsubscribe = observer.subscribe(() => {})
  return { client, reads, unsubscribe, refresher: new ResourceRefresher(client) }
}

test("读取在途时的持续失效不取消读取，每次结果都写入缓存，结束后只补读一次", async () => {
  const { client, reads, unsubscribe, refresher } = setup()
  try {
    refresher.invalidate(key)
    refresher.invalidate(key)
    refresher.invalidate(key)
    assert.equal(reads.length, 1)

    reads[0].resolve("v1")
    await flush()
    assert.equal(client.getQueryData(key), "v1")
    assert.equal(reads.length, 2)

    refresher.invalidate(key)
    reads[1].resolve("v2")
    await flush()
    assert.equal(client.getQueryData(key), "v2")
    assert.equal(reads.length, 3)

    reads[2].resolve("v3")
    await flush()
    assert.equal(client.getQueryData(key), "v3")
    assert.equal(reads.length, 3)
  } finally {
    unsubscribe()
  }
})

test("其他入口发起的在途读取结束后补读一次", async () => {
  const { client, reads, unsubscribe, refresher } = setup()
  try {
    void client.refetchQueries({ queryKey: key })
    refresher.invalidate(key)
    assert.equal(reads.length, 1)
    reads[0].resolve("v1")
    await flush()
    assert.equal(client.getQueryData(key), "v1")
    assert.equal(reads.length, 2)
    reads[1].resolve("v2")
    await flush()
    assert.equal(client.getQueryData(key), "v2")
    assert.equal(reads.length, 2)
  } finally {
    unsubscribe()
  }
})

test("断网暂停的读取不反复补读，网络恢复并完成后只补读一次", async () => {
  const { client, reads, unsubscribe, refresher } = setup()
  // 挂载后查询缓存才订阅网络状态，暂停的读取在恢复联网时继续。
  client.mount()
  onlineManager.setOnline(false)
  try {
    void client.refetchQueries({ queryKey: key })
    assert.equal(client.getQueryState(key)?.fetchStatus, "paused")
    refresher.invalidate(key)
    refresher.invalidate(key)
    await flush()
    assert.equal(reads.length, 0)

    onlineManager.setOnline(true)
    await flush()
    assert.equal(reads.length, 1)
    reads[0].resolve("v1")
    await flush()
    assert.equal(client.getQueryData(key), "v1")
    assert.equal(reads.length, 2)
    reads[1].resolve("v2")
    await flush()
    assert.equal(client.getQueryData(key), "v2")
    assert.equal(reads.length, 2)
  } finally {
    onlineManager.setOnline(true)
    unsubscribe()
    client.unmount()
  }
})

test("读取失败的查询在重试时重读，成功后不再重试", async () => {
  const { client, reads, unsubscribe, refresher } = setup()
  try {
    refresher.invalidate(key)
    reads[0].reject(new Error("network"))
    await flush()
    assert.equal(client.getQueryState(key)?.status, "error")

    refresher.retry()
    assert.equal(reads.length, 2)
    reads[1].resolve("v1")
    await flush()
    assert.equal(client.getQueryData(key), "v1")

    refresher.retry()
    assert.equal(reads.length, 2)
  } finally {
    unsubscribe()
  }
})

test("未挂载的查询只标记失效，卸载后的查询不补读也不重试", async () => {
  const { client, reads, unsubscribe, refresher } = setup()
  client.setQueryData(["identity"], "identity")
  refresher.invalidate(["identity"])
  assert.equal(client.getQueryState(["identity"])?.isInvalidated, true)

  refresher.invalidate(key)
  refresher.invalidate(key)
  unsubscribe()
  reads[0].reject(new Error("network"))
  await flush()
  refresher.retry()
  assert.equal(reads.length, 1)
})
