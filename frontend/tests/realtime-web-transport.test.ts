/** 用可控响应流验证 Web 端事件流的行解析、错误响应、空闲超时与关闭。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import {
  createWebRealtimeTransport,
  realtimeIdleTimeoutMs,
} from "../src/api/realtime/web-transport.ts"

/** 等待流读取等异步回调执行若干轮。 */
async function flush(rounds = 5) {
  for (let round = 0; round < rounds; round += 1) {
    await new Promise((resolve) => setImmediate(resolve))
  }
}

/** 构造可逐块写入的事件流响应。 */
function eventStream() {
  const encoder = new TextEncoder()
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({
    start(value) {
      controller = value
    },
  })
  return {
    response: new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } }),
    push: (text: string) => controller.enqueue(encoder.encode(text)),
    end: () => controller.close(),
  }
}

/** 使用给定响应与请求头打开一条事件流。 */
function open(response: Response, headers: () => Record<string, string> | undefined = () => ({ Authorization: "Bearer token" })) {
  const requests: RequestInit[] = []
  const frames: string[] = []
  const closed: unknown[] = []
  const transport = createWebRealtimeTransport({
    url: "/api/realtime",
    fetch: async (_url, init) => {
      requests.push(init)
      return response
    },
    headers,
    responseError: (status, body) => ({ status, body }),
  })
  const close = transport.open({ frame: (frame) => frames.push(frame), closed: (error) => closed.push(error) })
  return { requests, frames, closed, close }
}

test("按行解析跨数据块的 data 事件，忽略空行并在流结束时回调", async () => {
  const stream = eventStream()
  const client = open(stream.response)
  await flush()
  assert.deepEqual(client.requests[0].headers, { Authorization: "Bearer token" })

  stream.push('data: {"type":')
  stream.push('"ping"}\n\n')
  stream.push("data: a\r\n\ndata: b\n")
  await flush()
  assert.deepEqual(client.frames, ['{"type":"ping"}', "a", "b"])
  assert.deepEqual(client.closed, [])

  stream.end()
  await flush()
  assert.deepEqual(client.closed, [undefined])
})

test("非 2xx 响应把状态码与错误体交给错误转换", async () => {
  const body = { error: { state: "login", message: "login required" } }
  const client = open(new Response(JSON.stringify(body), { status: 401 }))
  await flush()
  assert.deepEqual(client.closed, [{ status: 401, body }])
})

test("登录会话已变化时不发起请求", async () => {
  const client = open(eventStream().response, () => undefined)
  await flush()
  assert.equal(client.requests.length, 0)
  assert.equal(client.closed.length, 1)
})

test("关闭后中止请求且不再回调", async () => {
  const stream = eventStream()
  const client = open(stream.response)
  await flush()
  client.close()
  assert.equal(client.requests[0].signal?.aborted, true)
  await flush()
  assert.deepEqual(client.frames, [])
  assert.deepEqual(client.closed, [])
})

test("超过空闲时限未收到数据时结束事件流，收到数据后重新计时", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const stream = eventStream()
  const client = open(stream.response)
  await flush()

  t.mock.timers.tick(realtimeIdleTimeoutMs - 1)
  stream.push("data: ping\n")
  await flush()
  t.mock.timers.tick(realtimeIdleTimeoutMs - 1)
  assert.deepEqual(client.closed, [])

  t.mock.timers.tick(1)
  await flush()
  assert.equal(client.closed.length, 1)
  assert.match(String(client.closed[0]), /idle timeout/)
  assert.equal(client.requests[0].signal?.aborted, true)
})
