/** 账号共用一条 jetcast 连接，成员页面、运行流与跨工作区通知订阅同一连接的事件。 */
import { connect, UnauthorizedError, type Echo, type Channel, type ChannelState } from "@runforyou/jetcast"
import { isApiError, serverURL, sessionRequestMeta } from "@/api/client"
import type { RealtimeConnection, RealtimeMember } from "@/api/generated/contract"
import { getRealtimeConnection, getSyncHeads, loadIdentity } from "@/api/generated/operations"
import { sessionPath } from "@/api/session"
import { currentSessionGeneration, requestWorkspace, subscribeSessionGeneration } from "@/api/session-scope"
import { decodeServerFrame, type RealtimeServerFrame } from "./protocol"
import type { RealtimeClientEvent, RealtimeState } from "./realtime-client"

type RunListener = { receive: (data: unknown) => void; state: (state: ChannelState) => void; error: (error: unknown) => void }

type Scope = "member" | "account"

/** 在业务请求因代次隔离保持未决时，仍向 SDK 交付取消结果。 */
function untilCanceled<T>(request: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const cancel = () => reject(new UnauthorizedError())
    if (signal.aborted) { cancel(); return }
    signal.addEventListener("abort", cancel, { once: true })
    request.then(resolve, reject).finally(() => signal.removeEventListener("abort", cancel))
  })
}

/** 共享连接管理器持有 SDK 连接和本代次的请求取消信号。 */
class MemberConnection {
  private readonly scopes = new Set<Scope>()
  private readonly listeners = new Map<Scope, Set<(event: RealtimeClientEvent) => void>>()
  private echo: Echo | undefined
  private controller: AbortController | undefined
  private channels = new Map<string, Channel>()
  private runListeners = new Map<string, Set<RunListener>>()
  private runChannels = new Map<string, Channel>()
  private state: RealtimeState = "disconnected"
  private attempt = 0
  private syncing = false
  private syncAgain = false
  private snapshotRetry: ReturnType<typeof setTimeout> | undefined
  private openRetry: ReturnType<typeof setTimeout> | undefined

  /** 会话代次改变时释放全部旧请求和旧连接。 */
  constructor() {
    subscribeSessionGeneration(() => {
      this.scopes.clear()
      this.close()
    })
  }

  /** 为成员页面或账号通知提供共享连接的生命周期接口。 */
  client(scope: Scope) {
    const shared = this
    return {
      get state() { return shared.state },
      subscribe: (listener: (event: RealtimeClientEvent) => void) => {
        const listeners = this.listeners.get(scope) ?? new Set()
        this.listeners.set(scope, listeners)
        listeners.add(listener)
        return () => { listeners.delete(listener) }
      },
      start: () => {
        const added = !this.scopes.has(scope)
        this.scopes.add(scope)
        if (!this.controller) void this.open()
        else if (added && this.state === "ready") void this.snapshot()
      },
      stop: () => {
        this.scopes.delete(scope)
        if (!this.demand()) this.close()
      },
      restart: () => { this.close(); if (this.demand()) void this.open() },
      resume: () => { if (!this.controller && this.demand()) void this.open() },
    }
  }

  /** 为精确运行频道登记订阅，多个观察者共用同一个 SDK 频道。 */
  watchRun(name: string, listener: RunListener) {
    const listeners = this.runListeners.get(name) ?? new Set<RunListener>()
    this.runListeners.set(name, listeners)
    listeners.add(listener)
    if (!this.controller) void this.open()
    else if (this.echo) this.subscribeRuns(this.echo)
    const channel = this.runChannels.get(name)
    if (channel?.state === "subscribed") queueMicrotask(() => {
      if (listeners.has(listener)) listener.state({ state: "subscribed", recovered: false })
    })
    return () => {
      listeners.delete(listener)
      if (!listeners.size) {
        this.runListeners.delete(name)
        this.runChannels.get(name)?.leave()
        this.runChannels.delete(name)
      }
      if (!this.demand()) this.close()
    }
  }

  /** 返回是否仍有账号或运行观察者持有连接。 */
  private demand() { return this.scopes.size > 0 || this.runListeners.size > 0 }

  /** 安装本代次的运行频道并把恢复与失权状态交给各运行处理。 */
  private subscribeRuns(echo: Echo) {
    const attempt = this.attempt
    for (const [name, listeners] of this.runListeners) {
      if (this.runChannels.has(name)) continue
      const channel = echo.private(name)
      this.runChannels.set(name, channel)
      const current = () => this.echo === echo && this.attempt === attempt && this.runChannels.get(name) === channel
      channel.listenAll((data: unknown) => {
        if (current()) for (const listener of listeners) listener.receive(data)
      })
      channel.onState((state) => {
        if (current()) for (const listener of [...listeners]) listener.state(state)
      })
      void channel.ready().then(() => {
        if (current()) for (const listener of [...listeners]) listener.state({ state: "subscribed", recovered: false })
      }).catch(() => undefined)
    }
  }

  /** 使旧回调失效，取消 HTTP 请求与恢复快照重试，并关闭已建立的 SDK 连接。 */
  private close() {
    this.attempt += 1
    this.controller?.abort()
    this.controller = undefined
    const echo = this.echo
    this.echo = undefined
    this.channels.clear()
    this.runChannels.clear()
    for (const listeners of this.runListeners.values()) {
      for (const listener of [...listeners]) listener.state({ state: "interrupted" })
    }
    this.syncing = false
    this.syncAgain = false
    clearTimeout(this.snapshotRetry)
    this.snapshotRetry = undefined
    clearTimeout(this.openRetry)
    this.openRetry = undefined
    void echo?.close()
    this.setState("disconnected")
  }

  /** 读取服务端配置后创建 SDK；网络重连与退避由 SDK 负责。 */
  private async open() {
    const attempt = ++this.attempt
    const generation = currentSessionGeneration()
    const controller = new AbortController()
    this.controller = controller
    const current = () => attempt === this.attempt && generation === currentSessionGeneration() && !controller.signal.aborted
    let sessionFailed = false
    this.setState("connecting")
    try {
      const config = await untilCanceled(getRealtimeConnection(controller.signal), controller.signal)
      if (!current()) return
      const address = new URL(config.path, serverURL() || window.location.origin)
      address.protocol = address.protocol === "https:" ? "wss:" : "ws:"
      // 首次连接取消由令牌回调终止 SDK 重试；已在途的连接完成后立即关闭。
      const echo = await connect({
        servers: address.href,
        prefix: config.prefix,
        getToken: async () => {
          if (!current()) throw new UnauthorizedError()
          try {
            await untilCanceled(getRealtimeConnection(controller.signal), controller.signal)
          } catch (error) {
            if (!current()) throw new UnauthorizedError()
            if (isApiError(error) && sessionPath(error.state)) {
              sessionFailed = true
              this.emitBoth({ type: "session_error", error })
              this.failRuns(error)
              throw new UnauthorizedError()
            }
            throw error
          }
          const meta = sessionRequestMeta()
          if (!current() || !meta?.token) throw new UnauthorizedError()
          return meta.token
        },
      })
      if (!current()) { await echo.close(); return }
      this.echo = echo
      echo.onStatus((status) => {
        if (!current()) return
        if (status === "stopped") {
          // 服务端撤销旧授权后复核业务会话；有效账号以新的成员范围重新连接。
          this.close()
          if (!sessionFailed && this.demand()) void this.open()
        } else if (status === "reconnecting") {
          this.setState("backoff")
          // 连接中断时立即取消运行观察者的快照请求与计时器。
          for (const listeners of this.runListeners.values()) {
            for (const listener of [...listeners]) listener.state({ state: "interrupted" })
          }
        } else if (status === "connected") this.subscribe(echo, current)
      })
      this.subscribe(echo, current)
    } catch (error) {
      if (!current()) return
      this.close()
      if (isApiError(error) && sessionPath(error.state)) {
        this.setState("stopped")
        this.emitBoth({ type: "session_error", error })
        this.failRuns(error)
      } else {
        this.setState("backoff")
        console.warn("建立成员实时连接失败", error)
        this.openRetry = setTimeout(() => {
          this.openRetry = undefined
          if (!this.controller && this.demand()) void this.open()
        }, 1000)
      }
    }
  }

  /** 把连接级认证错误交给每个运行的会话恢复入口。 */
  private failRuns(error: unknown) {
    for (const listeners of this.runListeners.values()) {
      for (const listener of [...listeners]) listener.error(error)
    }
  }

  /** 按认证响应中的精确频道安装订阅，订阅就绪后读取当前快照。 */
  private subscribe(echo: Echo, current: () => boolean) {
    this.subscribeRuns(echo)
    const config = echo.info as RealtimeConnection
    const wanted = new Set<string>()
    for (const member of config.members) {
      for (const name of [member.channel, member.inboxChannel, member.typingChannel, member.inboxTypingChannel]) {
        wanted.add(name)
        if (this.channels.has(name)) continue
        const channel = echo.private(name)
        this.channels.set(name, channel)
        channel.listenAll((data) => {
          if (current()) this.receive(member, data)
        })
        channel.onState((state) => {
          if (!current()) return
          if (state.state === "subscribed") void this.snapshot()
          // 被拒绝的频道已由 SDK 释放，移出订阅表后其余频道继续同步。
          else if (state.state === "denied" && this.channels.get(name) === channel) {
            console.warn("成员实时频道订阅被拒绝", name)
            this.channels.delete(name)
            void this.snapshot()
          }
        })
      }
    }
    for (const [name, channel] of this.channels) {
      if (!wanted.has(name)) { channel.leave(); this.channels.delete(name) }
    }
    // 每个频道订阅成功或被拒绝后进入 ready，被拒绝的频道已在状态回调中移出。
    void Promise.allSettled([...this.channels.values()].map((channel) => channel.ready())).then(() => {
      if (current()) { this.setState("ready"); void this.snapshot() }
    })
  }

  /** 订阅完成后读取同步探针并刷新无探针数据，合并同时就绪的频道回调。 */
  private async snapshot() {
    if (this.echo?.status !== "connected" || [...this.channels.values()].some((channel) => channel.state !== "subscribed")) return
    if (this.syncing) { this.syncAgain = true; return }
    clearTimeout(this.snapshotRetry)
    this.snapshotRetry = undefined
    const attempt = this.attempt
    this.syncing = true
    try {
      if (this.scopes.has("member") && requestWorkspace()) {
        // 身份复核将暂停或移出的当前工作区恢复到现有工作区选择入口。
        await loadIdentity(this.controller?.signal)
        const heads = await getSyncHeads(this.controller?.signal)
        if (attempt === this.attempt) this.emit("member", { type: "frame", frame: { type: "server_hello", connectionId: this.echo?.socketId ?? "", syncHeads: heads } })
      }
      if (attempt === this.attempt) this.emit("account", { type: "resync" })
    } catch (error) {
      if (attempt !== this.attempt) return
      if (isApiError(error) && sessionPath(error.state)) {
        this.syncAgain = false
        this.emitBoth({ type: "session_error", error })
      } else {
        // 快照恢复独立重试，成功后仍交付成员与账号的完整刷新信号。
        this.syncAgain = false
        this.snapshotRetry = setTimeout(() => {
          this.snapshotRetry = undefined
          if (attempt === this.attempt) void this.snapshot()
        }, 1000)
      }
    } finally {
      if (attempt === this.attempt) {
        this.syncing = false
        if (this.syncAgain) { this.syncAgain = false; void this.snapshot() }
      }
    }
  }

  /** 解码成员事件，并在前端派生跨工作区变化和用户通知。 */
  private receive(member: RealtimeMember, data: unknown) {
    const result = decodeServerFrame(JSON.stringify(data))
    if (result.status !== "frame") return
    const frame = result.frame
    if (frame.type === "user_notification") this.emit("account", { type: "frame", frame })
    else if (member.workspaceId === requestWorkspace()) this.emit("member", { type: "frame", frame })
    let activity: RealtimeServerFrame | undefined
    switch (frame.type) {
      case "conversation_changed": activity = { type: "workspace_activity", workspaceId: member.workspaceId, kind: frame.type, conversationId: frame.conversationId, changes: frame.changes }; break
      case "conversation_removed":
      case "conversation_state_changed": activity = { type: "workspace_activity", workspaceId: member.workspaceId, kind: frame.type, conversationId: frame.conversationId, changes: [] }; break
      case "identity_profile_changed": activity = { type: "workspace_activity", workspaceId: member.workspaceId, kind: frame.type, conversationId: "", changes: [] }; break
    }
    if (activity) this.emit("account", { type: "frame", frame: activity })
  }

  /** 更新共享连接状态。 */
  private setState(state: RealtimeState) {
    if (this.state === state) return
    this.state = state
    this.emitBoth({ type: "state", state })
  }

  /** 向两个用途的订阅方交付连接级事件。 */
  private emitBoth(event: RealtimeClientEvent) { this.emit("member", event); this.emit("account", event) }

  /** 只向仍持有本用途连接的订阅方交付事件。 */
  private emit(scope: Scope, event: RealtimeClientEvent) {
    if (!this.scopes.has(scope)) return
    for (const listener of this.listeners.get(scope) ?? []) listener(event)
  }
}

const connection = new MemberConnection()
export const realtimeClient = connection.client("member")
export const workspaceActivityClient = connection.client("account")

/** 运行流在账号共享连接上订阅精确频道。 */
export function watchRunChannel(workspaceID: string, runID: string, listener: RunListener) {
  return connection.watchRun(`w.${workspaceID}.runs.${runID}`, listener)
}
