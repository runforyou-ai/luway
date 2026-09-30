/** 用可控传输、代次与计时器验证成员实时事件流的状态机、退避重连和过期回调隔离。 */
import assert from "node:assert/strict"
import { test, type TestContext } from "node:test"
import {
  RealtimeClient,
  type RealtimeClientEvent,
  type RealtimeStreamHandlers,
} from "../src/api/realtime/realtime-client.ts"

type FakeStream = { handlers: RealtimeStreamHandlers; closed: boolean }

const hello = JSON.stringify({
  v: 1,
  type: "server_hello",
  data: { connectionId: "conn", syncHeads: { conversationCount: 0, conversationChecksum: "0", identityProfileVersion: "0", pinOrderVersion: "0" } },
})
const sessionError = new Error("login required")

/** 构造使用假传输、假代次与假计时器的客户端。 */
function setup(t: TestContext, random = () => 0) {
  t.mock.method(console, "info", () => {})
  t.mock.method(console, "warn", () => {})
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const streams: FakeStream[] = []
  const listeners = new Set<() => void>()
  let generation = 0
  const events: RealtimeClientEvent[] = []
  const client = new RealtimeClient({
    transport: {
      open(handlers) {
        const stream = { handlers, closed: false }
        streams.push(stream)
        return () => {
          stream.closed = true
        }
      },
    },
    generation: {
      current: () => generation,
      subscribe: (listener) => {
        listeners.add(listener)
        return () => listeners.delete(listener)
      },
    },
    isSessionError: (error) => error === sessionError,
    random,
  })
  client.subscribe((event) => events.push(event))
  return {
    client,
    streams,
    events,
    advanceGeneration() {
      generation += 1
      for (const listener of listeners) listener()
    },
  }
}

test("代次变化立即关闭事件流，旧事件流晚到的事件与结束回调都被丢弃", (t) => {
  const { client, streams, events, advanceGeneration } = setup(t)
  client.start()
  advanceGeneration()
  assert.equal(streams[0].closed, true)
  assert.equal(client.state, "disconnected")

  streams[0].handlers.frame(hello)
  streams[0].handlers.closed(new Error("network"))
  t.mock.timers.tick(60_000)
  assert.equal(client.state, "disconnected")
  assert.equal(streams.length, 1)
  assert.equal(events.some((event) => event.type === "frame"), false)
})

test("旧事件流结束回调与重连交错时只保留当前事件流", (t) => {
  const { client, streams } = setup(t)
  client.start()
  streams[0].handlers.frame(hello)
  assert.equal(client.state, "ready")

  streams[0].handlers.closed()
  assert.equal(client.state, "backoff")
  t.mock.timers.tick(1_000)
  assert.equal(streams.length, 2)

  streams[0].handlers.closed(new Error("late"))
  streams[0].handlers.frame(hello)
  assert.equal(client.state, "connecting")
  streams[1].handlers.frame(hello)
  assert.equal(client.state, "ready")
  t.mock.timers.tick(60_000)
  assert.equal(streams.length, 2)
})

test("回到前台重建已就绪的事件流，连续触发只建立一条", (t) => {
  const { client, streams } = setup(t)
  client.start()
  streams[0].handlers.frame(hello)
  assert.equal(client.state, "ready")

  client.restart()
  assert.equal(streams[0].closed, true)
  assert.equal(streams.length, 2)
  assert.equal(client.state, "connecting")

  // 前台恢复与网络恢复成对触发时，尚未就绪的连接不被再次替换。
  client.restart()
  client.resume()
  assert.equal(streams.length, 2)
  assert.equal(streams[1].closed, false)
})

test("未就绪的事件流不因回到前台被重建", (t) => {
  const { client, streams } = setup(t)
  client.start()
  client.restart()
  assert.equal(streams.length, 1)

  streams[0].handlers.closed(new Error("network"))
  assert.equal(client.state, "backoff")
  client.restart()
  assert.equal(streams.length, 1)
  // 退避期间仍由 resume 跳过剩余等待。
  client.resume()
  assert.equal(streams.length, 2)
})

test("停止后的事件流不因回到前台被重建", (t) => {
  const { client, streams } = setup(t)
  client.start()
  streams[0].handlers.closed(sessionError)
  assert.equal(client.state, "stopped")
  client.restart()
  t.mock.timers.tick(60_000)
  assert.equal(streams.length, 1)
})

test("会话错误停止重连并交给订阅方恢复入口", (t) => {
  const { client, streams, events } = setup(t)
  client.start()
  streams[0].handlers.closed(sessionError)
  assert.equal(client.state, "stopped")
  assert.deepEqual(events.at(-1), { type: "session_error", error: sessionError })
  client.resume()
  t.mock.timers.tick(60_000)
  assert.equal(streams.length, 1)
})

test("协议主版本不受支持时关闭事件流，网络恢复与前台恢复不能重启", (t) => {
  const { client, streams } = setup(t)
  client.start()
  streams[0].handlers.frame(JSON.stringify({ v: 2, type: "server_hello", data: {} }))
  assert.equal(client.state, "stopped")
  assert.equal(streams[0].closed, true)
  client.resume()
  t.mock.timers.tick(60_000)
  assert.equal(streams.length, 1)
})

test("无法解析的事件被忽略，事件流继续工作", (t) => {
  const { client, streams, events } = setup(t)
  client.start()
  streams[0].handlers.frame("{")
  streams[0].handlers.frame(hello)
  assert.equal(client.state, "ready")
  assert.equal(events.filter((event) => event.type === "frame").length, 1)
})

test("退避等待取上限一半到上限之间并逐次翻倍，收到 server_hello 后重置", (t) => {
  const { client, streams } = setup(t)
  client.start()

  for (const [index, delay] of [500, 1_000, 2_000, 4_000, 8_000, 15_000, 15_000].entries()) {
    streams[index].handlers.closed()
    t.mock.timers.tick(delay - 1)
    assert.equal(streams.length, index + 1, `第 ${index + 1} 次失败未到等待时间`)
    t.mock.timers.tick(1)
    assert.equal(streams.length, index + 2, `第 ${index + 1} 次失败到达等待时间`)
  }

  streams.at(-1)!.handlers.frame(hello)
  streams.at(-1)!.handlers.closed()
  t.mock.timers.tick(500)
  assert.equal(streams.length, 9)
})

test("抖动系数取到上限时等待完整上限", (t) => {
  const { client, streams } = setup(t, () => 1)
  client.start()
  streams[0].handlers.closed()
  t.mock.timers.tick(999)
  assert.equal(streams.length, 1)
  t.mock.timers.tick(1)
  assert.equal(streams.length, 2)
})

test("start、stop、start 重新连接，同一代次重复 start 不重复建立", (t) => {
  const { client, streams } = setup(t)
  client.start()
  client.start()
  assert.equal(streams.length, 1)
  client.stop()
  assert.equal(streams[0].closed, true)
  assert.equal(client.state, "disconnected")
  client.start()
  assert.equal(streams.length, 2)
  assert.equal(client.state, "connecting")
})

test("网络恢复与回到前台同时触发时只立即重连一次", (t) => {
  const { client, streams } = setup(t)
  client.start()
  streams[0].handlers.closed()
  client.resume()
  client.resume()
  assert.equal(streams.length, 2)
  t.mock.timers.tick(60_000)
  assert.equal(streams.length, 2)
})

test("stop 取消等待中的重连", (t) => {
  const { client, streams } = setup(t)
  client.start()
  streams[0].handlers.closed()
  client.stop()
  t.mock.timers.tick(60_000)
  assert.equal(streams.length, 1)
})
