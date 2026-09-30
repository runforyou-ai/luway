/** 原生端成员事件流传输：串行执行 Go 侧连接与断开，按连接编号投递事件。 */
import type { RealtimeStreamHandlers, RealtimeTransport } from "./realtime-client.ts"

/** 可取消的原生连接调用，成功时返回本地连接编号。 */
export type NativeRealtimeConnect = Promise<string> & { cancel: () => void }

/** Go 侧事件流的连接、断开与事件监听。 */
type NativeRealtimeBridge = {
  connect: () => NativeRealtimeConnect
  disconnect: (connectionId: string) => Promise<void>
  onFrame: (listener: (connectionId: string, frame: string) => void) => () => void
  onClosed: (listener: (connectionId: string) => void) => () => void
}

type NativeEvent = { connectionId: string; frame?: string }

/**
 * 按连接编号分流原生事件：编号返回前到达的事件先缓存，bind 后只回放并投递属于本连接的事件；
 * 事件流结束或 release 后停止接收并注销监听。
 */
function createNativeEventReceiver(
  handlers: RealtimeStreamHandlers,
  subscribe: (deliver: (event: NativeEvent) => void) => () => void,
) {
  let closed = false
  let connectionId: string | undefined
  const early: NativeEvent[] = []
  const unsubscribe = subscribe(deliver)

  /** 投递属于本连接的事件，事件流结束时释放监听。 */
  function deliver(event: NativeEvent) {
    if (closed) return
    if (connectionId === undefined) {
      early.push(event)
      return
    }
    if (event.connectionId !== connectionId) return
    if (event.frame !== undefined) {
      handlers.frame(event.frame)
      return
    }
    release()
    handlers.closed()
  }

  /** 停止接收事件并注销监听。 */
  function release() {
    closed = true
    unsubscribe()
  }

  return {
    get closed() {
      return closed
    },
    get connectionId() {
      return connectionId
    },
    /** 确定本连接编号并回放已缓存的事件。 */
    bind(id: string) {
      connectionId = id
      for (const event of early.splice(0)) deliver(event)
    },
    release,
  }
}

/** 创建原生端事件流传输；断开按连接编号关闭对应事件流，连接与断开按调用顺序串行执行。 */
export function createNativeRealtimeTransport(bridge: NativeRealtimeBridge): RealtimeTransport {
  // 上一次连接请求及其清理完成后才发起下一次连接，旧请求晚到时不会替换新事件流。
  let operations: Promise<void> = Promise.resolve()

  return {
    open(handlers) {
      let pending: NativeRealtimeConnect | undefined
      const receiver = createNativeEventReceiver(handlers, (deliver) => {
        const stopFrame = bridge.onFrame((id, frame) => deliver({ connectionId: id, frame }))
        const stopClosed = bridge.onClosed((id) => deliver({ connectionId: id }))
        return () => {
          stopFrame()
          stopClosed()
        }
      })

      operations = operations
        .then(async () => {
          if (receiver.closed) return
          pending = bridge.connect()
          let id: string
          try {
            id = await pending
          } catch (error) {
            if (!receiver.closed) {
              receiver.release()
              handlers.closed(error)
            }
            return
          } finally {
            pending = undefined
          }
          if (receiver.closed) {
            // 调用方已关闭时连接可能仍已建立，断开后才允许下一次连接。
            await bridge
              .disconnect(id)
              .catch((error: unknown) => console.warn("断开过期的实时事件流失败", error))
            return
          }
          receiver.bind(id)
        })
        .catch((error: unknown) => console.error("处理原生实时事件流连接失败", error))

      return () => {
        if (receiver.closed) return
        const established = receiver.connectionId
        receiver.release()
        pending?.cancel()
        if (established !== undefined) {
          operations = operations.then(() =>
            bridge
              .disconnect(established)
              .catch((error: unknown) => console.warn("断开实时事件流失败", error)),
          )
        }
      }
    },
  }
}

/** Go 侧运行过程流的连接、断开与事件监听；事件携带运行编号，取得本地流编号前据此过滤。 */
type NativeRunStreamBridge = {
  connect: (runId: string) => NativeRealtimeConnect
  disconnect: (connectionId: string) => Promise<void>
  onFrame: (listener: (connectionId: string, runId: string, frame: string) => void) => () => void
  onClosed: (listener: (connectionId: string, runId: string) => void) => () => void
}

/** 创建原生端运行过程流传输；每条流独立建立与关闭，按本地流编号分流事件。 */
export function createNativeRunStreamTransport(bridge: NativeRunStreamBridge, runId: string): RealtimeTransport {
  return {
    open(handlers) {
      // 并发的运行过程流共用同一组 Wails 事件，取得本地流编号前先按运行编号过滤。
      const receiver = createNativeEventReceiver(handlers, (deliver) => {
        const stopFrame = bridge.onFrame((id, run, frame) => {
          if (run === runId) deliver({ connectionId: id, frame })
        })
        const stopClosed = bridge.onClosed((id, run) => {
          if (run === runId) deliver({ connectionId: id })
        })
        return () => {
          stopFrame()
          stopClosed()
        }
      })

      const pending = bridge.connect(runId)
      void pending.then(
        (id) => {
          if (receiver.closed) {
            // 调用方已关闭时流可能仍已建立，建立后立即断开。
            void bridge.disconnect(id).catch((error: unknown) => console.warn("断开过期的运行过程流失败", error))
            return
          }
          receiver.bind(id)
        },
        (error: unknown) => {
          if (receiver.closed) return
          receiver.release()
          handlers.closed(error)
        },
      )

      return () => {
        if (receiver.closed) return
        const established = receiver.connectionId
        receiver.release()
        if (established === undefined) {
          pending.cancel()
          return
        }
        void bridge.disconnect(established).catch((error: unknown) => console.warn("断开运行过程流失败", error))
      }
    },
  }
}
