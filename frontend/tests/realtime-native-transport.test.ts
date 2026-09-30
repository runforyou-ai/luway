/** 用可控的原生连接调用与事件验证连接串行、事件回放和监听清理。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import {
  createNativeRealtimeTransport,
  type NativeRealtimeConnect,
} from "../src/api/realtime/native-transport.ts"

type Connect = PromiseWithResolvers<string> & { cancelled: boolean }

/** 等待已排队的微任务与 I/O 回调执行完毕。 */
function flush() {
  return new Promise((resolve) => setImmediate(resolve))
}

/** 构造记录连接、断开和监听状态的原生桥。 */
function setup() {
  const frameListeners = new Set<(connectionId: string, frame: string) => void>()
  const closedListeners = new Set<(connectionId: string) => void>()
  const connects: Connect[] = []
  const log: string[] = []
  const transport = createNativeRealtimeTransport({
    connect() {
      const connect = { ...Promise.withResolvers<string>(), cancelled: false }
      connects.push(connect)
      log.push("connect")
      return Object.assign(connect.promise, {
        cancel: () => {
          connect.cancelled = true
        },
      }) as NativeRealtimeConnect
    },
    async disconnect(connectionId: string) {
      log.push(`disconnect:${connectionId}`)
    },
    onFrame(listener) {
      frameListeners.add(listener)
      return () => frameListeners.delete(listener)
    },
    onClosed(listener) {
      closedListeners.add(listener)
      return () => closedListeners.delete(listener)
    },
  })
  return {
    connects,
    log,
    listenerCount: () => frameListeners.size + closedListeners.size,
    emitFrame: (connectionId: string, frame: string) => [...frameListeners].forEach((listener) => listener(connectionId, frame)),
    emitClosed: (connectionId: string) => [...closedListeners].forEach((listener) => listener(connectionId)),
    open() {
      const frames: string[] = []
      const closed: unknown[] = []
      const close = transport.open({ frame: (frame) => frames.push(frame), closed: (error) => closed.push(error) })
      return { frames, closed, close }
    },
  }
}

test("连接编号返回前到达的事件在返回后只回放属于本连接的部分", async () => {
  const bridge = setup()
  const stream = bridge.open()
  await flush()
  bridge.emitFrame("conn-a", "hello")
  bridge.emitFrame("conn-old", "stale")
  bridge.connects[0].resolve("conn-a")
  await flush()
  assert.deepEqual(stream.frames, ["hello"])

  bridge.emitFrame("conn-a", "ping")
  bridge.emitFrame("conn-old", "stale")
  assert.deepEqual(stream.frames, ["hello", "ping"])
})

test("连接编号返回前到达的结束事件回放为流结束并注销监听", async () => {
  const bridge = setup()
  const stream = bridge.open()
  await flush()
  bridge.emitFrame("conn-a", "hello")
  bridge.emitClosed("conn-a")
  bridge.connects[0].resolve("conn-a")
  await flush()
  assert.deepEqual(stream.frames, ["hello"])
  assert.deepEqual(stream.closed, [undefined])
  assert.equal(bridge.listenerCount(), 0)

  stream.close()
  await flush()
  assert.deepEqual(bridge.log, ["connect"])
})

test("旧连接请求晚到时先断开旧连接，新连接在其后发起", async () => {
  const bridge = setup()
  const first = bridge.open()
  await flush()
  first.close()
  assert.equal(bridge.connects[0].cancelled, true)

  const second = bridge.open()
  await flush()
  assert.equal(bridge.connects.length, 1)

  bridge.connects[0].resolve("conn-a")
  await flush()
  assert.deepEqual(bridge.log, ["connect", "disconnect:conn-a", "connect"])

  bridge.connects[1].resolve("conn-b")
  await flush()
  bridge.emitFrame("conn-a", "stale")
  bridge.emitFrame("conn-b", "hello")
  assert.deepEqual(first.frames, [])
  assert.deepEqual(first.closed, [])
  assert.deepEqual(second.frames, ["hello"])
})

test("断开已建立的连接与新连接交错时按调用顺序串行执行", async () => {
  const bridge = setup()
  const first = bridge.open()
  await flush()
  bridge.connects[0].resolve("conn-a")
  await flush()

  first.close()
  bridge.open()
  await flush()
  assert.deepEqual(bridge.log, ["connect", "disconnect:conn-a", "connect"])
  assert.equal(bridge.listenerCount(), 2)
})

test("连接失败时回调错误并注销监听", async () => {
  const bridge = setup()
  const stream = bridge.open()
  await flush()
  const error = new Error("unavailable")
  bridge.connects[0].reject(error)
  await flush()
  assert.deepEqual(stream.closed, [error])
  assert.equal(bridge.listenerCount(), 0)

  bridge.open()
  await flush()
  assert.equal(bridge.connects.length, 2)
})

test("关闭后不再回调且注销全部监听", async () => {
  const bridge = setup()
  const stream = bridge.open()
  await flush()
  bridge.connects[0].resolve("conn-a")
  await flush()
  stream.close()
  stream.close()
  bridge.emitFrame("conn-a", "late")
  bridge.emitClosed("conn-a")
  await flush()
  assert.deepEqual(stream.frames, [])
  assert.deepEqual(stream.closed, [])
  assert.equal(bridge.listenerCount(), 0)
  assert.deepEqual(bridge.log, ["connect", "disconnect:conn-a"])
})
