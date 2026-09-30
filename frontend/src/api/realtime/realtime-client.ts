/** 成员实时事件流内核：维护连接状态、按抖动退避重连，并按登录会话代次丢弃过期连接与回调。 */
import { decodeServerFrame, type RealtimeServerFrame } from "./protocol.ts"

/** 事件流连接状态；stopped 表示会话错误或协议不受支持，只能由 start 重新开始。 */
export type RealtimeState = "disconnected" | "connecting" | "ready" | "backoff" | "stopped"

/** 传输层回调；回调总是在 open 返回之后、关闭函数调用之前发生。 */
export type RealtimeStreamHandlers = {
  frame: (text: string) => void
  closed: (error?: unknown) => void
}

/** 建立一条事件流并返回幂等的关闭函数；建立失败与流结束都经 closed 回调。 */
export type RealtimeTransport = {
  open: (handlers: RealtimeStreamHandlers) => () => void
}

/** 客户端向订阅方发布的状态变化、服务端事件与会话错误。 */
export type RealtimeClientEvent =
  | { type: "state"; state: RealtimeState }
  | { type: "frame"; frame: RealtimeServerFrame }
  | { type: "session_error"; error: unknown }

type RealtimeClientOptions = {
  transport: RealtimeTransport
  generation: { current: () => number; subscribe: (listener: () => void) => () => void }
  isSessionError: (error: unknown) => boolean
  random?: () => number
}

const backoffBaseMs = 1_000
const backoffMaxMs = 30_000

/** 第 failures 次连续失败后的重连等待时间：上限从 1 秒起翻倍至 maxMs，取上限的一半到上限之间，同时断开的连接错开重连。 */
export function reconnectDelay(failures: number, maxMs: number, random: () => number) {
  const ceiling = Math.min(maxMs, backoffBaseMs * 2 ** (failures - 1))
  return ceiling / 2 + (random() * ceiling) / 2
}

/** 应用实例内唯一的成员事件流客户端。 */
export class RealtimeClient {
  private readonly options: RealtimeClientOptions
  private readonly listeners = new Set<(event: RealtimeClientEvent) => void>()
  private current: RealtimeState = "disconnected"
  private attempt = 0
  private generation = -1
  private failures = 0
  private close: (() => void) | undefined
  private timer: ReturnType<typeof setTimeout> | undefined

  /** 创建客户端，登录会话代次变化时立即关闭事件流并取消重连。 */
  constructor(options: RealtimeClientOptions) {
    this.options = options
    options.generation.subscribe(() => {
      if (this.current !== "disconnected" && this.generation !== options.generation.current()) {
        this.stop()
      }
    })
  }

  /** 返回当前连接状态。 */
  get state() {
    return this.current
  }

  /** 订阅客户端事件，返回取消订阅函数。 */
  subscribe(listener: (event: RealtimeClientEvent) => void) {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /** 按当前登录会话代次开始连接；当前代次已在连接、就绪或等待重连时不重复建立。 */
  start() {
    const generation = this.options.generation.current()
    if (this.current !== "disconnected" && this.current !== "stopped" && this.generation === generation) {
      return
    }
    this.halt("disconnected")
    this.generation = generation
    this.failures = 0
    this.connect()
  }

  /** 关闭事件流并取消等待中的重连，之后到达的传输回调一律丢弃。 */
  stop() {
    this.halt("disconnected")
  }

  /** 关闭已就绪的事件流并立即重新连接，系统挂起后按可能失活处理该连接；其他状态不受影响。 */
  restart() {
    if (this.current !== "ready") {
      return
    }
    this.halt("disconnected")
    this.start()
  }

  /** 网络恢复或回到前台时跳过剩余退避等待；其他状态不受影响。 */
  resume() {
    if (this.current !== "backoff") {
      return
    }
    clearTimeout(this.timer)
    this.timer = undefined
    this.connect()
  }

  /** 使当前连接尝试失效，关闭事件流、取消重连计时并进入指定状态。 */
  private halt(state: "disconnected" | "stopped") {
    this.attempt += 1
    clearTimeout(this.timer)
    this.timer = undefined
    const close = this.close
    this.close = undefined
    close?.()
    this.setState(state)
  }

  /** 发起一次连接尝试，只接收属于本次尝试的传输回调。 */
  private connect() {
    this.attempt += 1
    const attempt = this.attempt
    this.setState("connecting")
    this.close = this.options.transport.open({
      frame: (text) => {
        if (attempt === this.attempt) this.receive(text)
      },
      closed: (error) => {
        if (attempt === this.attempt) this.closed(error)
      },
    })
  }

  /** 解码并发布一条服务端事件；协议主版本不受支持时终止连接。 */
  private receive(text: string) {
    const result = decodeServerFrame(text)
    switch (result.status) {
      case "unsupported_version":
        console.warn("实时事件流协议主版本不受支持，停止重连")
        this.halt("stopped")
        return
      case "invalid":
        console.warn("忽略无法解析的实时事件", { reason: result.reason })
        return
      case "ignored":
        return
    }
    if (result.frame.type === "server_hello") {
      this.failures = 0
      this.setState("ready")
    }
    this.emit({ type: "frame", frame: result.frame })
  }

  /** 处理事件流结束：会话错误交给订阅方恢复入口，其余情况按退避等待后重连。 */
  private closed(error: unknown) {
    this.close = undefined
    if (this.options.isSessionError(error)) {
      this.halt("stopped")
      this.emit({ type: "session_error", error })
      return
    }
    this.failures += 1
    const delay = reconnectDelay(this.failures, backoffMaxMs, this.options.random ?? Math.random)
    console.info("实时事件流已断开，等待重连", { delay: Math.round(delay), error })
    this.setState("backoff")
    this.timer = setTimeout(() => {
      this.timer = undefined
      this.connect()
    }, delay)
  }

  /** 更新连接状态并在变化时发布。 */
  private setState(state: RealtimeState) {
    if (this.current === state) {
      return
    }
    this.current = state
    console.info("实时事件流状态变化", { state })
    this.emit({ type: "state", state })
  }

  /** 向全部订阅方发布客户端事件。 */
  private emit(event: RealtimeClientEvent) {
    for (const listener of [...this.listeners]) {
      listener(event)
    }
  }
}
