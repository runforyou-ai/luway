/** 应用实时网关客户端，统一维护连接、业务频道和断线恢复游标。 */
export class UnauthorizedError extends Error {}

export type ChannelState = { state: "subscribed" | "interrupted" | "denied"; recovered?: boolean; reason?: string }
type Status = "connecting" | "connected" | "reconnecting" | "stopped"
type Options = { url: string; getToken: () => Promise<string> }
type Frame = { op: string; channel?: string; epoch?: string; position?: number; sequence?: number; id?: string; data?: unknown; info?: unknown; socket?: string; reason?: string; recovered?: boolean }

/** Channel 保存频道状态、事件监听器与恢复位置。 */
export class Channel {
  state = "interrupted"
  epoch = ""
  position = 0
  private readonly seen = new Set<string>()
  private readonly events = new Set<(data: unknown) => void>()
  private readonly states = new Set<(state: ChannelState) => void>()
  private readonly waiters = new Set<{ resolve: () => void; reject: (error: Error) => void }>()

  /** 创建一个由指定客户端管理的频道。 */
  constructor(readonly client: Client, readonly name: string) {}

  /** 登记业务事件监听器。 */
  listenAll(fn: (data: unknown) => void) { this.events.add(fn); return () => { this.events.delete(fn) } }

  /** 登记频道状态监听器并交付当前就绪状态。 */
  onState(fn: (state: ChannelState) => void) {
    this.states.add(fn)
    if (this.state === "subscribed") fn({ state: "subscribed" })
    return () => { this.states.delete(fn) }
  }

  /** 等待订阅完成，拒绝时返回认证错误。 */
  ready(): Promise<void> {
    if (this.state === "subscribed") return Promise.resolve()
    if (this.state === "denied" || this.client.status === "stopped") return Promise.reject(new UnauthorizedError())
    return new Promise((resolve, reject) => { this.waiters.add({ resolve, reject }) })
  }

  /** 根据服务端帧推进恢复位置并交付事件。 */
  receive(frame: Frame) {
    if (frame.op === "event") {
      if (frame.sequence && frame.epoch === this.epoch && frame.sequence <= this.position) return
      if (frame.sequence) { this.position = frame.sequence; this.epoch = frame.epoch ?? "" }
      if (frame.id && frame.sequence) {
        if (this.seen.has(frame.id)) return
        this.seen.add(frame.id)
        if (this.seen.size > 1024) this.seen.delete(this.seen.values().next().value!)
      }
      for (const fn of [...this.events]) fn(frame.data)
    } else if (frame.op === "checkpoint") {
      this.epoch = frame.epoch ?? ""
      this.position = frame.position ?? 0
    } else if (frame.op === "subscribed") {
      this.epoch = frame.epoch ?? ""
      this.position = frame.position ?? 0
      this.transition({ state: "subscribed", recovered: frame.recovered ?? false, reason: frame.reason })
    } else if (frame.op === "denied") this.transition({ state: "denied" })
  }

  /** 发布频道状态并完成等待订阅的调用。 */
  transition(state: ChannelState) {
    this.state = state.state
    if (state.state !== "interrupted") {
      for (const waiter of this.waiters) {
        if (state.state === "subscribed") waiter.resolve()
        else waiter.reject(new UnauthorizedError())
      }
      this.waiters.clear()
    }
    for (const fn of [...this.states]) fn(state)
  }

  /** 取消频道并释放等待者。 */
  leave() {
    this.client.send({ op: "unsub", channel: this.name })
    this.client.channels.delete(this.name)
    for (const waiter of this.waiters) waiter.reject(new Error("subscription closed"))
    this.waiters.clear()
    this.events.clear()
    this.states.clear()
  }
}

/** Client 持有一条经过票据认证的应用 WebSocket 连接。 */
export class Client {
  status: Status = "connecting"
  info: unknown
  socketId = ""
  readonly channels = new Map<string, Channel>()
  private readonly statuses = new Set<(status: Status) => void>()
  private socket: WebSocket | undefined
  private stopped = false
  private retry: ReturnType<typeof setTimeout> | undefined

  /** 创建实时客户端。 */
  constructor(private readonly options: Options) {}

  /** 使用新票据连接网关，首帧响应后交付连接状态。 */
  async open(): Promise<void> {
    const ticket = await this.options.getToken()
    if (this.stopped) throw new UnauthorizedError()
    await new Promise<void>((resolve, reject) => {
      const socket = new WebSocket(this.options.url)
      this.socket = socket
      let ready = false
      const timeout = setTimeout(() => { if (this.socket === socket) this.socket = undefined; socket.close(); reject(new Error("realtime connection timeout")) }, 10000)
      socket.onopen = () => { socket.send(JSON.stringify({ op: "auth", ticket })) }
      socket.onmessage = (event) => {
        if (this.socket !== socket || this.stopped) return
        let frame: Frame
        try { frame = JSON.parse(event.data) as Frame } catch { socket.close(); return }
        if (frame.op === "ready") {
          ready = true
          clearTimeout(timeout)
          this.info = frame.info
          this.socketId = frame.socket ?? ""
          this.transition("connected")
          for (const channel of this.channels.values()) this.subscribe(channel)
          resolve()
        } else if (frame.op === "bye") {
          if (!ready) reject(new UnauthorizedError())
          socket.close()
        } else if (frame.channel) this.channels.get(frame.channel)?.receive(frame)
      }
      socket.onerror = () => { if (!ready) reject(new Error("realtime connection failed")) }
      socket.onclose = () => {
        clearTimeout(timeout)
        if (!ready) reject(new Error("realtime connection closed"))
        if (this.socket !== socket || this.stopped) return
        this.socket = undefined
        if (ready) this.reconnect()
      }
    })
  }

  /** 断线后用新票据重连，并保留频道恢复位置。 */
  private reconnect() {
    if (this.stopped || this.retry) return
    this.transition("reconnecting")
    for (const channel of this.channels.values()) channel.transition({ state: "interrupted" })
    this.retry = setTimeout(() => {
      this.retry = undefined
      void this.open().catch((error: unknown) => {
        if (error instanceof UnauthorizedError) void this.close()
        else this.reconnect()
      })
    }, 1000)
  }

  /** 发布连接状态。 */
  private transition(status: Status) { this.status = status; for (const fn of [...this.statuses]) fn(status) }

  /** 登记连接状态监听器。 */
  onStatus(fn: (status: Status) => void) { this.statuses.add(fn); return () => { this.statuses.delete(fn) } }

  /** 返回一个业务私有频道。 */
  private(name: string): Channel {
    const existing = this.channels.get(name)
    if (existing) return existing
    const channel = new Channel(this, name)
    this.channels.set(name, channel)
    queueMicrotask(() => { if (this.channels.get(name) === channel && this.status === "connected") this.subscribe(channel) })
    return channel
  }

  /** 将保存的恢复游标发送到网关。 */
  private subscribe(channel: Channel) { this.send({ op: "sub", channel: channel.name, epoch: channel.epoch, position: channel.position }) }

  /** 在连接可用时发送控制帧。 */
  send(frame: Frame) {
    if (this.socket?.readyState === WebSocket.OPEN) {
      try { this.socket.send(JSON.stringify(frame)); return } catch { this.socket.close() }
    }
    if (frame.op === "sub" && this.status === "connected") this.reconnect()
  }

  /** 终止重连并释放全部订阅。 */
  async close() {
    if (this.stopped) return
    this.stopped = true
    clearTimeout(this.retry)
    this.retry = undefined
    this.socket?.close()
    this.socket = undefined
    for (const channel of [...this.channels.values()]) channel.leave()
    this.transition("stopped")
  }
}

/** connect 建立一个应用实时连接。 */
export async function connect(options: Options): Promise<Client> {
  const client = new Client(options)
  try { await client.open(); return client } catch (error) { await client.close(); throw error }
}
